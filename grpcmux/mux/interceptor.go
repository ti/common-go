package mux

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/ti/common-go/tools/stacktrace"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/metadata"

	"github.com/ti/common-go/tools/ip"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genproto/googleapis/api/httpbody"
	grpcMetadata "google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"google.golang.org/grpc/codes"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/log"
)

var noLogMethod = map[string]bool{
	http.MethodConnect: true,
	http.MethodOptions: true,
	http.MethodTrace:   true,
}

var noLogPath = map[string]bool{
	"/healthz":                           true,
	"/favicon.ico":                       true,
	"/readyz":                            true,
	pbhealth.Health_Check_FullMethodName: true,
}

func defaultInterceptor(opts *options) func(http.Handler) http.Handler {
	corsHandler := newCORSHandler(&opts.cors)
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if corsHandler != nil {
				if corsHandler(w, r) {
					return
				}
			}
			if opts.methodOverride {
				overrideMethod(r)
			}
			logMethod := r.Method
			if r.Method == http.MethodHead {
				// HEAD is GET without a body (RFC 9110): route, authenticate
				// and run it as the GET it mirrors, so it answers with that
				// GET's status and headers: 200 for a page, the auth error
				// for a protected route, whatever an unrouted GET answers for
				// an unrouted path. The copy leaves the server's own request
				// a HEAD, so net/http sends no body. Handlers that must tell
				// the two apart use IsHead.
				r = r.WithContext(context.WithValue(r.Context(), headKey{}, true))
				r.Method = http.MethodGet
			}
			if noLogMethod[r.Method] || noLogPath[r.URL.Path] {
				h.ServeHTTP(w, r)
				return
			}
			// A route that serves an RPC under another path (REST) is
			// resolved once, here: authentication exemptions and the log's
			// action follow the RPC, not the path it happens to be bound to.
			var rpcMethod string
			if opts.routeResolver != nil {
				r, rpcMethod = opts.routeResolver(r)
			}
			logger := log.Default(false)
			var responseStatus int
			defer func() {
				if opts.recovery {
					if rec := recover(); rec != nil {
						err := recoverRequest(logger, rec, logMethod)
						opts.writeError(w, r, err)
						return
					}
				}
				logRequest(logger, responseStatus, logMethod)
			}()
			// reject answers a request the middleware itself refuses (in
			// practice: authentication) and records what the access log shows.
			reject := func(err error) {
				st := convertErrorToStatus(err)
				logger.Inject(map[string]any{"code": int(st.Code()), "error": st.Message()})
				responseStatus = opts.writeError(w, r, err)
			}
			var err error
			ctx := log.NewContextWithLogger(r.Context(), logger)
			queryHeaderAdapter(r, opts.queryAccessToken)
			if opts.httpAuthFunc != nil {
				ctx, err = opts.httpAuthFunc(ctx, r)
				if err != nil {
					reject(err)
					return
				}
			}
			ctx = medataCtx(ctx, r)
			// Inject request-derived log fields (action/ip/peer/protocol/request_id)
			// BEFORE auth so that auth rejections (401/403) still produce a full
			// log line. Auth-derived fields (user_id/client_id/...) are added after
			// a successful auth by injectAuthInfo below.
			ctx = injectRequestInfo(ctx, r, logger, rpcMethod, opts.clientIPOf(r))
			if opts.preAuth != nil {
				if st := opts.preAuth(w, r.WithContext(ctx)); st != 0 {
					responseStatus = st
					return
				}
			}
			if opts.authFunc != nil && !opts.noAuth(r.URL.Path) && (rpcMethod == "" || !opts.noAuth(rpcMethod)) {
				ctx, err = opts.authFunc(ctx)
				if err != nil {
					reject(err)
					return
				}
			}
			ctx = injectAuthInfo(ctx, logger)
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

// defaultAllowHeaders are the request headers a cross-origin browser client
// may send: the library's own, plus what Connect, gRPC-Web and their
// compression negotiation use (the set connectrpc.com/cors recommends:
// X-Grpc-Web and X-User-Agent are sent by every gRPC-Web client).
var defaultAllowHeaders = "Authorization, Content-Type, Accept, " +
	"X-Project-Id, X-Device-Id, X-Request-Id, X-Request-Timestamp, " +
	"Connect-Protocol-Version, Connect-Timeout-Ms, Connect-Accept-Encoding, Connect-Content-Encoding, " +
	"Grpc-Timeout, Grpc-Accept-Encoding, Grpc-Encoding, X-Grpc-Web, X-User-Agent, " +
	"Accept-Encoding, Content-Encoding"

// defaultExposeHeaders are the response headers browser JavaScript may read.
// Without Grpc-Status, Grpc-Message and Grpc-Status-Details-Bin a gRPC-Web
// client cannot see the status of a trailers-only (error) response; the
// encoding headers let a client decompress Connect and gRPC-Web bodies.
var defaultExposeHeaders = "Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin, " +
	"Grpc-Encoding, Connect-Content-Encoding, Content-Encoding, X-Request-Id"

// newCORSHandler builds a CORS handler function from the given config.
// Returns nil when CORS is disabled.
func newCORSHandler(cfg *CORSConfig) func(http.ResponseWriter, *http.Request) bool {
	if cfg.Disabled {
		return nil
	}

	// Pre-compute the allowed-origins lookup set.
	allowAll := len(cfg.AllowedOrigins) == 0 // empty treated as allow-all
	allowedSet := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		if o == "*" {
			allowAll = true
		} else {
			allowedSet[strings.ToLower(o)] = true
		}
	}

	// Pre-compute the Allow-Headers value.
	allowHeaders := defaultAllowHeaders
	if len(cfg.AllowedHeaders) > 0 {
		allowHeaders = allowHeaders + ", " + strings.Join(cfg.AllowedHeaders, ", ")
	}

	// Pre-compute the Expose-Headers value.
	exposeHeaders := defaultExposeHeaders
	if len(cfg.ExposeHeaders) > 0 {
		exposeHeaders = exposeHeaders + ", " + strings.Join(cfg.ExposeHeaders, ", ")
	}

	return func(w http.ResponseWriter, r *http.Request) bool {
		if filepath.Ext(r.URL.Path) == "" {
			w.Header().Set("Cache-Control", "no-cache, no-store, max-age=0, must-revalidate")
		}

		origin := r.Header.Get("Origin")
		if origin != "" {
			if uri, err := url.Parse(origin); err == nil {
				if uri.Host != r.Host {
					originLower := strings.ToLower(origin)
					if allowAll || allowedSet[originLower] {
						w.Header().Set("Access-Control-Allow-Credentials", "true")
						w.Header().Set("Access-Control-Allow-Origin", origin)
						w.Header().Set("Access-Control-Allow-Methods", "POST, GET, PUT, DELETE, PATCH, OPTIONS")
						w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
						w.Header().Set("Access-Control-Expose-Headers", exposeHeaders)
						w.Header().Set("Vary", "Origin, Accept-Encoding")
						w.Header().Set("Access-Control-Max-Age", "86400")
					}
				}
			}
		}

		// A preflight is answered here. HEAD is not: it is served as the
		// GET it mirrors (see defaultInterceptor), not with a blanket 200.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return true
		}
		return false
	}
}

type headKey struct{}

// IsHead reports whether the request was sent as HEAD. The middleware hands
// a HEAD to the handlers as a GET (the response body is discarded by
// net/http), so r.Method does not tell.
func IsHead(r *http.Request) bool {
	head, _ := r.Context().Value(headKey{}).(bool)
	return head || r.Method == http.MethodHead
}

// overridableMethods are the methods X-HTTP-Method-Override may select.
var overridableMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// overrideMethod applies X-HTTP-Method-Override, the convention for clients
// that can only send POST. It is honored on POST requests only, and only for
// GET, PUT, PATCH and DELETE: a GET (which a cross-site page can trigger with
// a plain link or image) must never be turned into a write.
func overrideMethod(r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	if m := strings.ToUpper(r.Header.Get("X-HTTP-Method-Override")); overridableMethods[m] {
		r.Method = m
	}
}

func queryHeaderAdapter(r *http.Request, accessToken bool) {
	query := r.URL.Query()
	if auth := r.Header.Get("Authorization"); auth == "" && accessToken {
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

// rpcAction reduces an HTTP request path to its first two segments so it can be
// used as a low-cardinality "action" log field, matching the "/{Service}/{Method}"
// shape the gRPC/ConnectRPC reporter already emits.
//
// A ConnectRPC/gRPC path IS "/pkg.Service/Method" (exactly two segments), so it
// is returned unchanged. A hand-mounted HTTP route shaped as
// "/pkg.Service/Method/{id}/…" collapses to "/pkg.Service/Method", dropping the
// high-cardinality trailing parameters (project ids, task ids, file paths) that
// would otherwise explode the distinct-action count in logs and any downstream
// aggregation. Paths with fewer than two segments are returned as-is.
func rpcAction(path string) string {
	if path == "" {
		return path
	}
	// Find the slash that ends the second segment: skip the leading slash, then
	// the first segment, then look for the next slash.
	first := strings.IndexByte(path[1:], '/')
	if first < 0 {
		return path // only one segment, e.g. "/healthz"
	}
	second := strings.IndexByte(path[1+first+1:], '/')
	if second < 0 {
		return path // exactly two segments, e.g. "/pkg.Service/Method"
	}
	return path[:1+first+1+second]
}

// injectRequestInfo injects request-derived log fields (action/ip/peer/protocol/
// request_id) into the request-scoped logger and incoming metadata. It runs
// BEFORE authentication so that even auth-rejected requests (401) emit a complete
// log line. Handlers can add more fields later via log.Inject(ctx, ...).
//
// action is the request path reduced to its first two segments (rpcAction), so a
// hand-mounted HTTP route with dynamic trailing parameters logs the same
// low-cardinality "/{Service}/{Method}" shape the gRPC reporter emits.
//
// ip is the client address the server worked out (WithClientIP): the peer
// unless it is a trusted proxy. It replaces any "ip" the caller sent as a
// header, and no X-Forwarded-For value is read or written here.
//
// rpcMethod is the full method of a request the route resolver matched (a REST
// route); it is then the action, so a call logs the same action on every
// protocol and path variables never reach it.
func injectRequestInfo(ctx context.Context, r *http.Request, logger log.Logger, rpcMethod, clientIP string,
) context.Context {
	action := rpcMethod
	if action == "" {
		action = rpcAction(r.URL.Path)
	}
	logData := map[string]any{
		"action":   action,
		"ip":       clientIP,
		"protocol": r.Proto,
		"peer":     r.RemoteAddr,
	}
	requestID := r.Header.Get(requestIDHeader)
	if requestID == "" {
		requestID = uuid.New().String()
		r.Header.Set(requestIDHeader, requestID)
	}
	logData[requestIDTag] = requestID
	logger.Inject(logData)
	logger.Inject(TraceFields(ctx))
	md := metadata.ExtractIncoming(ctx)
	for k, v := range logData {
		md.Set(k, v.(string))
	}
	return ip.NewContextWithClientIP(md.ToIncoming(ctx), clientIP)
}

// TraceFields returns the log fields that correlate a log line with its
// OpenTelemetry trace: trace_id and span_id of the span in ctx. It returns
// nil when there is no valid span context (tracing off, or no tracer provider
// installed), so nothing empty is ever logged.
func TraceFields(ctx context.Context) map[string]any {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return nil
	}
	return map[string]any{
		"trace_id": sc.TraceID().String(),
		"span_id":  sc.SpanID().String(),
	}
}

// injectAuthInfo injects identity fields (project_id/client_id/device_id/user_id/
// organization_id) into the request-scoped logger once authentication has
// succeeded. Request-derived fields are already added by injectRequestInfo.
func injectAuthInfo(ctx context.Context, logger log.Logger) context.Context {
	authInfo, ok := AuthInfoFromContext(ctx)
	if !ok {
		return ctx
	}
	logData := map[string]any{}
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
	if len(logData) == 0 {
		return ctx
	}
	logger.Inject(logData)
	md := metadata.ExtractIncoming(ctx)
	for k, v := range logData {
		md.Set(k, v.(string))
	}
	return md.ToIncoming(ctx)
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
				// Log at most logBodyLimit bytes, and hand the handler the
				// whole body: what was read for the log, then the rest of the
				// stream. Logging must never change the request.
				body := r.Body
				buf, err := io.ReadAll(io.LimitReader(body, logBodyLimit))
				if len(buf) > 0 {
					logData[requestTag] = string(buf)
				}
				var rest io.Reader = body
				if err != nil {
					rest = errReader{err} // the handler sees the read error too
				}
				r.Body = bodyReader{Reader: io.MultiReader(bytes.NewReader(buf), rest), Closer: body}
			} else if r.URL.RawQuery != "" {
				logData[requestTag] = r.URL.RawQuery
			}
		}
		log.Inject(ctx, logData)
		return data
	}
}

// logBodyLimit is how much of a request body is copied into the access log.
const logBodyLimit = 1024000

// bodyReader reads from Reader and closes the original body.
type bodyReader struct {
	io.Reader
	io.Closer
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

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
