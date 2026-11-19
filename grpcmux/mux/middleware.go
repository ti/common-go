package mux

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// WriteHTTPErrorResponse set HTTP status code and write error description to the body.
func WriteHTTPErrorResponse(w http.ResponseWriter, r *http.Request, err error) {
	WriteHTTPErrorResponseWithMarshaler(w, err, defaultOptions.errorMarshaler, defaultOptions.newErrorBody)
}

// WriteHTTPErrorResponseWithMarshaler set HTTP status code and write error description to the body.
// The details are rendered as the package defaults say (see
// [WithTypedErrorDetails] and [SetDefaultOptions]).
func WriteHTTPErrorResponseWithMarshaler(w http.ResponseWriter, err error, marshaler runtime.Marshaler,
	newErrorBody func(grpcStatus *status.Status, statusCodeStr string) proto.Message,
) {
	writeHTTPError(w, err, marshaler, newErrorBody, defaultOptions.typedErrorDetails)
}

func writeHTTPError(w http.ResponseWriter, err error, marshaler runtime.Marshaler,
	newErrorBody func(grpcStatus *status.Status, statusCodeStr string) proto.Message, typedDetails bool,
) {
	s, ok := status.FromError(err)
	if !ok {
		s = status.New(codes.Unknown, err.Error())
	}
	if marshaler == nil {
		marshaler = defaultOptions.errorMarshaler
	}
	buf, merr := newErrorBytes(s, marshaler, newErrorBody, typedDetails)
	if merr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := io.WriteString(w, getFallback(false)); err != nil {
			grpclog.Infof("Failed to write response: %v", err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(codeToStatus(s.Code()))
	_, _ = w.Write(buf)
}

func newErrorBytes(s *status.Status, marshaler runtime.Marshaler,
	newErrorBody func(grpcStatus *status.Status, statusCodeStr string) proto.Message, typedDetails bool,
) ([]byte, error) {
	errorCodeStr := codeToError(s.Code())
	body := newErrorBody(s, errorCodeStr)
	buf, merr := marshaler.Marshal(body)
	if merr != nil {
		return nil, merr
	}
	// By default a detail is the bare JSON of its message, and a detail
	// with no set fields ("{}") is left out: the wire format existing clients
	// were written against. With typed details each one is
	// google.protobuf.Any JSON, the message's fields plus "@type", so a
	// client can tell an ErrorInfo from a RetryInfo (the Connect error body
	// names the type too), and none is left out for being empty. Either way
	// a detail whose type is not linked into the binary cannot be rendered
	// and is skipped.
	var detailsJSON [][]byte
	if typedDetails {
		for _, d := range s.Proto().GetDetails() {
			if b, err := marshaler.Marshal(d); err == nil && len(b) > 2 {
				detailsJSON = append(detailsJSON, b)
			}
		}
	} else {
		for _, v := range s.Details() {
			if b, err := marshaler.Marshal(v); err == nil && len(b) > 2 {
				detailsJSON = append(detailsJSON, b)
			}
		}
	}
	if len(detailsJSON) > 0 {
		buf = buf[0 : len(buf)-1]
		buf = append(buf, []byte(`,"details":[`)...)
		details := bytes.Join(detailsJSON, []byte(","))
		buf = append(buf, details...)
		buf = append(buf, []byte(`]}`)...)
	}
	return buf, nil
}

// protocolErrorWriter writes errors in the Connect streaming, gRPC and
// gRPC-Web wire formats.
var protocolErrorWriter = connecthttp.NewErrorWriter()

// isRPCProtocolRequest reports whether r is unambiguously an RPC protocol
// request that cannot read a plain HTTP error: a Connect stream, gRPC or
// gRPC-Web. Connect unary requests are plain JSON/proto POSTs and are not
// distinguishable from REST, so they are not included.
func isRPCProtocolRequest(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	return strings.HasPrefix(ct, "application/grpc") || strings.HasPrefix(ct, "application/connect+")
}

// writeError answers a request the middleware rejects and returns the HTTP
// status it wrote.
func (o *options) writeError(w http.ResponseWriter, r *http.Request, err error) int {
	if o.protocolErrors && isRPCProtocolRequest(r) {
		if werr := protocolErrorWriter.Write(w, r, toConnectError(err)); werr != nil {
			grpclog.Infof("Failed to write response: %v", werr)
		}
		return http.StatusOK
	}
	writeHTTPError(w, err, o.errorMarshaler, o.newErrorBody, o.typedErrorDetails)
	return codeToStatus(convertErrorToStatus(err).Code())
}

// toConnectError converts a gRPC status error into a *connect.Error with the
// same code, message and details.
func toConnectError(err error) *connect.Error {
	st := convertErrorToStatus(err)
	cerr := connect.NewError(connect.Code(st.Code()), st.Message())
	for _, d := range st.Proto().GetDetails() {
		cerr = cerr.WithDetail(&connect.ErrorDetail{
			Type:  strings.TrimPrefix(d.GetTypeUrl(), "type.googleapis.com/"),
			Value: d.GetValue(),
		})
	}
	return cerr
}
