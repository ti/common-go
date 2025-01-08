package service

import (
	"context"
	"io"
	"net/http"

	pb "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"
	"github.com/ti/common-go/grpcmux/mux"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

// HelloStreamHTTP stream api http version
func (s *Server) HelloStreamHTTP(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		mux.WriteHTTPErrorResponse(w, r, err)
		return
	}
	in := &pb.Request{}
	err = protojson.Unmarshal(reqBody, in)
	if err != nil {
		mux.WriteHTTPErrorResponse(w, r, err)
		return
	}
	_, _ = w.Write([]byte("["))
	sender := &httpStreamSender{
		w:   w,
		ctx: r.Context(),
	}
	err = s.HelloStream(in, sender)
	if err != nil {
		mux.WriteHTTPErrorResponse(w, r, err)
		return
	}
	_, _ = w.Write([]byte("]"))
}

type httpStreamSender struct {
	w   http.ResponseWriter
	ctx context.Context
	grpc.ServerStream
	start bool
}

// Send for http
func (h *httpStreamSender) Send(resp *pb.Response) error {
	body, _ := protojson.Marshal(resp)
	if !h.start {
		h.start = true
	} else {
		_, err := h.w.Write([]byte(","))
		if err != nil {
			return err
		}
	}
	_, err := h.w.Write(body)
	h.w.(http.Flusher).Flush()
	return err
}

func (h *httpStreamSender) Context() context.Context {
	return h.ctx
}
