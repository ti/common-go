package mux

import (
	"bytes"
	"io"
	"net/http"

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
func WriteHTTPErrorResponseWithMarshaler(w http.ResponseWriter, err error, marshaler runtime.Marshaler,
	newErrorBody func(grpcStatus *status.Status, statusCodeStr string) proto.Message,
) {
	s, ok := status.FromError(err)
	if !ok {
		s = status.New(codes.Unknown, err.Error())
	}
	if marshaler == nil {
		marshaler = defaultOptions.errorMarshaler
	}
	buf, merr := newErrorBytes(s, marshaler, newErrorBody)
	if merr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := io.WriteString(w, fallback); err != nil {
			grpclog.Infof("Failed to write response: %v", err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(codeToStatus(s.Code()))
	_, _ = w.Write(buf)
}

func newErrorBytes(s *status.Status, marshaler runtime.Marshaler,
	newErrorBody func(grpcStatus *status.Status, statusCodeStr string) proto.Message,
) ([]byte, error) {
	errorCodeStr := codeToError(s.Code())
	body := newErrorBody(s, errorCodeStr)
	buf, merr := marshaler.Marshal(body)
	if merr != nil {
		return nil, merr
	}
	var detailsJSON [][]byte
	for _, v := range s.Details() {
		b, err := marshaler.Marshal(v)
		if err == nil && len(b) > 2 {
			detailsJSON = append(detailsJSON, b)
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
