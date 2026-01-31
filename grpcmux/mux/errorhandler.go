package mux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/ti/common-go/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/status"
)

const (
	fallbackSnakeCase = `{"error": "internal","error_description":"failed to marshal error message"}`
	fallbackCamelCase = `{"error": "internal","errorDescription":"failed to marshal error message"}`
)

func getFallback(useCamelCase bool) string {
	if useCamelCase {
		return fallbackCamelCase
	}
	return fallbackSnakeCase
}

func convertErrorToStatus(err error) *status.Status {
	if err == nil {
		return status.New(codes.OK, "")
	}
	if errors.Is(err, context.Canceled) {
		return status.New(codes.Canceled, err.Error())
	}
	return status.Convert(err)
}

func httpErrorHandler(opts *options) runtime.ErrorHandlerFunc {
	return func(ctx context.Context, mux *runtime.ServeMux, _ runtime.Marshaler,
		w http.ResponseWriter, r *http.Request, err error,
	) {
		w.Header().Del("Trailer")
		w.Header().Del("Transfer-Encoding")
		s := convertErrorToStatus(err)
		buf, errBytes := newErrorBytes(s, opts.errorMarshaler, opts.newErrorBody)
		if errBytes != nil {
			grpclog.Infof("Failed to marshal error message %q: %v", s, errBytes)
			w.WriteHeader(http.StatusInternalServerError)
			if _, err := io.WriteString(w, getFallback(opts.useCamelCase)); err != nil {
				grpclog.Infof("Failed to write response: %v", err)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		md, ok := runtime.ServerMetadataFromContext(ctx)
		if !ok {
			grpclog.Infof("Failed to extract ServerMetadata from context")
		}
		handleForwardResponseServerMetadata(w, mux, md)

		// RFC 7230 https://tools.ietf.org/html/rfc7230#section-4.1.2
		// Unless the request includes a TE header field indicating trailers
		// is acceptable, as described in Section 4.3, a server SHOULD NOT
		// generate trailer fields that it believes are necessary for the user
		// agent to receive.
		var wantsTrailers bool

		if te := r.Header.Get("TE"); strings.Contains(strings.ToLower(te), "trailers") {
			wantsTrailers = true
			handleForwardResponseTrailerHeader(w, md)
			w.Header().Set("Transfer-Encoding", "chunked")
		}
		w.WriteHeader(codeToStatus(s.Code()))
		log.Inject(r.Context(), map[string]any{
			"code":  int(s.Code()),
			"error": s.Message(),
		})
		if _, err := w.Write(buf); err != nil {
			grpclog.Infof("Failed to write response: %v", err)
		}
		if wantsTrailers {
			handleForwardResponseTrailer(w, md)
		}
	}
}

func routingErrorHandler(opts *options) runtime.RoutingErrorHandlerFunc {
	return func(ctx context.Context, mux *runtime.ServeMux, marshaler runtime.Marshaler,
		w http.ResponseWriter, r *http.Request, httpStatus int,
	) {
		if httpStatus == http.StatusNotFound {
			s := convertErrorToStatus(status.Error(codes.NotFound, "API NOT FOUND"))
			buf, errBytes := newErrorBytes(s, opts.errorMarshaler, opts.newErrorBody)
			if errBytes != nil {
				grpclog.Infof("Failed to marshal error message %q: %v", s, errBytes)
				w.WriteHeader(http.StatusInternalServerError)
				if _, err := io.WriteString(w, getFallback(opts.useCamelCase)); err != nil {
					grpclog.Infof("Failed to write response: %v", err)
				}
				return
			}
			if _, err := w.Write(buf); err != nil {
				grpclog.Infof("Failed to write response: %v", err)
			}
			log.Inject(r.Context(), map[string]any{
				"action": r.URL.Path,
				"code":   int(s.Code()),
				"error":  s.Message(),
			})
			return
		}
		runtime.DefaultRoutingErrorHandler(ctx, mux, marshaler, w, r, httpStatus)
	}
}

// handlerWithMiddleWares handler with middle wares.
func handlerWithMiddleWares(h http.Handler, middleWares ...func(http.Handler) http.Handler) http.Handler {
	lenMiddleWare := len(middleWares)
	for i := lenMiddleWare - 1; i >= 0; i-- {
		middleWare := middleWares[i]
		h = middleWare(h)
	}
	return h
}

func handleForwardResponseTrailerHeader(w http.ResponseWriter, md runtime.ServerMetadata) {
	for k := range md.TrailerMD {
		tKey := textproto.CanonicalMIMEHeaderKey(fmt.Sprintf("%s%s", runtime.MetadataTrailerPrefix, k))
		w.Header().Add("Trailer", tKey)
	}
}

func handleForwardResponseTrailer(w http.ResponseWriter, md runtime.ServerMetadata) {
	for k, vs := range md.TrailerMD {
		tKey := runtime.MetadataTrailerPrefix + k
		for _, v := range vs {
			w.Header().Add(tKey, v)
		}
	}
}

func handleForwardResponseServerMetadata(w http.ResponseWriter, _ *runtime.ServeMux, md runtime.ServerMetadata) {
	handleForwardResponseTrailer(w, md)
}

const (
	statusOKPrefix         = 2
	statusBadRequestPrefix = 4
)

// httpStatusCode the 2xxx is 200, the 4xxx is 400, the 5xxx is 500.
func httpStatusCode(code codes.Code) int {
	// http status codes can be error codes
	if code >= 200 && code < 599 {
		return int(code)
	}
	for code >= 10 {
		code /= 10
	}
	var httpStatusCode int
	switch code {
	case statusOKPrefix:
		httpStatusCode = http.StatusOK
	case statusBadRequestPrefix:
		httpStatusCode = http.StatusBadRequest
	default:
		httpStatusCode = http.StatusInternalServerError
	}
	return httpStatusCode
}

func codeToError(c codes.Code) string {
	errStr, ok := codesErrors[c]
	if ok {
		return errStr
	}
	return strconv.FormatInt(int64(c), 10)
}

// codesErrors some errors string for grpc codes.
var codesErrors = map[codes.Code]string{
	codes.OK:                 "ok",
	codes.Canceled:           "canceled",
	codes.Unknown:            "unknown",
	codes.InvalidArgument:    "invalid_argument",
	codes.DeadlineExceeded:   "deadline_exceeded",
	codes.NotFound:           "not_found",
	codes.AlreadyExists:      "already_exists",
	codes.PermissionDenied:   "permission_denied",
	codes.ResourceExhausted:  "resource_exhausted",
	codes.FailedPrecondition: "failed_precondition",
	codes.Aborted:            "aborted",
	codes.OutOfRange:         "out_of_range",
	codes.Unimplemented:      "unimplemented",
	codes.Internal:           "internal",
	codes.Unavailable:        "unavailable",
	codes.DataLoss:           "data_loss",
	codes.Unauthenticated:    "unauthenticated",
}

// RegisterErrorCodes set custom error codes for DefaultHTTPError
// for an exp: `grpcmux.RegisterErrorCodes(pb.ErrorCode_name)`
// SetCustomErrorCodes set custom error codes for DefaultHTTPError
// the map[int32]string is compact to protobuf’s ENMU_name
// 2*** HTTP status 200
// 4*** HTTP status 400
// 5*** AND other HTTP status 500
// For an exp:
// in proto
//
//	```enum CommonError {
//		captcha_required = 4001;
//		invalid_captcha = 4002;
//	}```
//
// in code
// grpcmux.RegisterErrorCodes(common.CommonError_name).
func RegisterErrorCodes(codeErrors map[int32]string) {
	for code, errorMsg := range codeErrors {
		codesErrors[codes.Code(code)] = errorMsg
	}
}

func codeToStatus(code codes.Code) int {
	st := int(code)
	if st > 100 {
		st = httpStatusCode(code)
	} else {
		st = runtime.HTTPStatusFromCode(code)
	}
	return st
}
