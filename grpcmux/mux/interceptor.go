package mux

import (
	"bytes"
	"context"
	"github.com/ti/common-go/tools/stacktrace"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	"github.com/google/uuid"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/metadata"
	"github.com/ti/common-go/tools/ip"

	"google.golang.org/genproto/googleapis/api/httpbody"
	grpcMetadata "google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/ti/common-go/log"
	"google.golang.org/grpc/codes"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

var noLogMethod = map[string]bool{
	http.MethodConnect: true,
	http.MethodOptions: true,
	http.MethodTrace:   true,
	http.MethodHead:    true,
}

var noLogPath = map[string]bool{
	"/healthz":                           true,
	"/favicon.ico":                       true,
	pbhealth.Health_Check_FullMethodName: true,
}

func defaultInterceptor(opts *options) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !opts.noCors {
				if enableCORS(w, r) {
					return
				}
			}
			if overrideMethod := r.Header.Get("X-HTTP-Method-Override"); overrideMethod != "" {
				r.Method = overrideMethod
			}
			if noLogMethod[r.Method] || noLogPath[r.URL.Path] {
				h.ServeHTTP(w, r)
				return
			}
			logger := log.Default(false)
			var responseStatus int
			defer func() {
				if opts.recovery {
					if rec := recover(); rec != nil {
						err := recoverRequest(logger, rec, r.Method)
						WriteHTTPErrorResponseWithMarshaler(w, err, opts.errorMarshaler, opts.newErrorBody)
						return
					}
				}
				logRequest(logger, responseStatus, r.Method)
			}()
			var err error
			ctx := log.NewContextWithLogger(r.Context(), logger)
			queryHeaderAdapter(r)
			if opts.httpAuthFunc != nil {
				ctx, err = opts.httpAuthFunc(ctx, r)
				if err != nil {
					responseStatus = http.StatusUnauthorized
					WriteHTTPErrorResponseWithMarshaler(w, err, opts.errorMarshaler, opts.newErrorBody)
					return
				}
			}
			ctx = medataCtx(ctx, r)
			if opts.authFunc != nil {
				var noAuth bool
				for _, v := range opts.noAuthPrefix {
					if strings.HasPrefix(r.URL.Path, v) {
						noAuth = true
					}
				}
				if !noAuth {
					ctx, err = opts.authFunc(ctx)
					if err != nil {
						responseStatus = http.StatusUnauthorized
						WriteHTTPErrorResponseWithMarshaler(w, err, opts.errorMarshaler, opts.newErrorBody)
						return
					}
				}
			}
			ctx = injectAuthInfo(ctx, r, logger)
			writer := &responseWriter{
				W:                 w,
				R:                 r,
				BodyReWriter:      opts.bodyReWriter,
				WithoutHTTPStatus: opts.withoutHTTPStatus,
				RequestID:         r.Header.Get(requestIDHeader),
			}
			h.ServeHTTP(writer, r.WithContext(ctx))
			responseStatus = writer.status
		})
	}
}

func enableCORS(w http.ResponseWriter, r *http.Request) bool {
	if filepath.Ext(r.URL.Path) == "" {
		w.Header().Set("Cache-Control", "no-cache, no-store, max-age=0, must-revalidate")
	}
	origin := r.Header.Get("Origin")
	if origin != "" {
		if uri, err := url.Parse(origin); err == nil {
			if uri.Host != r.Host {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "POST, GET, PUT, DELETE, PATCH, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, "+
					"X-Project-Id, X-Device-Id, X-Request-Id, X-Request-Timestamp")
				w.Header().Set("Vary", "Origin, Accept-Encoding")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
		}
	}
	if r.Method == http.MethodHead || r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return true
	}
	return false
}

func queryHeaderAdapter(r *http.Request) {
	query := r.URL.Query()
	if auth := r.Header.Get("Authorization"); auth == "" {
		if accessToken := query.Get("access_token"); accessToken != "" {
			r.Header.Set("Authorization", "Bearer "+accessToken)
		}
	}
	if captchaToken := query.Get("captcha_token"); captchaToken != "" {
		r.Header.Set("x-captcha-token", captchaToken)
	} else {
		for _, c := range r.Cookies() {
			if c.Name == "captcha_token" {
				r.Header.Set("x-captcha-token", c.Value)
			}
		}
	}
}

func injectAuthInfo(ctx context.Context, r *http.Request, logger log.Logger) context.Context {
	logData := map[string]any{
		"action": r.URL.Path,
	}
	if authInfo, ok := AuthInfoFromContext(ctx); ok {
		if id := authInfo.GetProjectID(); id != "" {
			logData["project_id"] = id
		}
		if id := authInfo.GetClientID(); id != "" {
			logData["client_id"] = id
		}
		if id := authInfo.GetDeviceID(); id != "" {
			logData["device_id"] = id
		}
		if id := authInfo.GetUserID(); id != "" {
			logData["user_id"] = id
		}
		if id := authInfo.GetOrganizationID(); id != "" {
			logData["organization_id"] = id
		}
	}
	ipAddr := ip.GetIPFromHTTPRequest(r)
	logData["ip"] = ipAddr
	logData["protocol"] = r.Proto
	logData["peer"] = r.RemoteAddr
	requestID := r.Header.Get(requestIDHeader)
	if requestID == "" {
		requestID = uuid.New().String()
		r.Header.Set(requestIDHeader, requestID)
	}
	logData[requestIDTag] = requestID
	logger.Inject(logData)
	md := metadata.ExtractIncoming(ctx)
	for k, v := range logData {
		md.Set(k, v.(string))
	}
	ctx = md.ToIncoming(ctx)
	return ctx
}

func recoverRequest(l log.Logger, rec any, method string) error {
	stack := stacktrace.Callers(4)
	l.With(map[string]any{
		"stack": stack,
		"code":  int(codes.Internal),
		"error": rec,
	}).Info(method)
	return status.Errorf(codes.Internal, "panic %s", rec)
}

func logRequest(l log.Logger, statusCode int, msg string) {
	if statusCode == 0 {
		statusCode = 200
	}
	var level logging.Level
	switch {
	case statusCode >= http.StatusInternalServerError:
		if statusCode == http.StatusNotImplemented {
			level = logging.LevelWarn
		} else {
			level = logging.LevelError
		}
	case statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError:
		level = logging.LevelWarn
	default:
		level = logging.LevelInfo
	}
	logger := l.With(map[string]any{
		"status": statusCode,
	})
	switch level {
	case logging.LevelInfo:
		logger.Info(msg)
	case logging.LevelWarn:
		logger.Warn(msg)
	case logging.LevelError:
		logger.Error(msg)
	default:
		logger.Debug(msg)
	}
}

func medaGetter(opts *options) func(ctx context.Context, r *http.Request) grpcMetadata.MD {
	return func(ctx context.Context, r *http.Request) grpcMetadata.MD {
		data := grpcMetadata.MD(metadata.ExtractIncoming(ctx))
		captchaToken := r.URL.Query().Get("captcha_token")
		if captchaToken != "" {
			data.Set("x-captcha-token", captchaToken)
		}
		if opts.noLog {
			return data
		}
		logData := map[string]any{}
		if method, ok := runtime.RPCMethod(ctx); ok {
			logData["method"] = method
		}
		if opts.logBody {
			if r.Method == http.MethodPost || r.Method == http.MethodPatch {
				buf, err := io.ReadAll(io.LimitReader(r.Body, 1024000))
				if err == nil && len(buf) > 0 {
					logData[requestTag] = string(buf)
					r.Body = io.NopCloser(bytes.NewBuffer(buf))
				}
			} else if r.URL.RawQuery != "" {
				logData[requestTag] = r.URL.RawQuery
			}
		}
		log.Inject(ctx, logData)
		return data
	}
}

func forwardResponser(opts *options) func(ctx context.Context, w http.ResponseWriter, message proto.Message) error {
	return func(ctx context.Context, w http.ResponseWriter, message proto.Message) error {
		if !opts.noLog && opts.logBody {
			bodyData, _ := protojson.Marshal(message)
			if len(bodyData) > 2048 {
				bodyData = bodyData[:2048]
			}
			log.Inject(ctx, map[string]any{
				"response": string(bodyData),
			})
		}
		if body, ok := message.(*httpbody.HttpBody); ok {
			if body.ContentType == typeLocation {
				location := string(body.Data)
				w.Header().Set(typeLocation, location)
				body.ContentType = "text/html; charset=utf-8"
				w.Header().Set("Content-Type", body.ContentType)
				w.WriteHeader(http.StatusFound)
				body.Data = []byte("<a href=\"" + htmlReplacer.Replace(location) + "\">Found</a>.\n")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=3600")
				w.Header().Set("Expires", time.Now().Add(time.Hour).Format(time.RFC1123))
			}
		}
		return nil
	}
}

func medataCtx(ctx context.Context, r *http.Request) context.Context {
	md := make(metadata.MD)
	for k, v := range r.Header {
		key := strings.ToLower(k)
		if len(v) > 0 && isPermanentMetaKey(key) || strings.HasPrefix(key, "x-") {
			md[key] = v
		}
	}
	const hostMD = "host"
	if _, ok := md[hostMD]; !ok {
		md[hostMD] = []string{r.Host}
	}
	return md.ToIncoming(ctx)
}
