package routerlimit

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/metadata"

	"github.com/ti/common-go/grpcmux/mux"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewHandler new router limit handler.
func NewHandler(limiter *Limiter) func(h http.Handler) http.Handler {
	if limiter.PersistenceFn == nil {
		limiter.PersistenceFn = newMemLimiter().AllowN
	}
	l := &LimitHandler{
		limiter: limiter,
	}
	l.responseWriter = func(w http.ResponseWriter, r *http.Request, block bool, limitMessage string) {
		var err error
		if block {
			err = status.Errorf(codes.Aborted, "rate limit aborted, %s", limitMessage)
		} else {
			err = status.Errorf(codes.ResourceExhausted, "rate limit exhausted, %s", limitMessage)
		}
		mux.WriteHTTPErrorResponse(w, r, err)
	}
	return func(h http.Handler) http.Handler {
		l.handler = h
		return l
	}
}

// ResponseWriterFunc the response writer func when rate limit exceeded.
type ResponseWriterFunc func(w http.ResponseWriter, r *http.Request, block bool, limitMessage string)

// LimitHandler the ratelimit handler.
type LimitHandler struct {
	handler        http.Handler
	limiter        *Limiter
	responseWriter ResponseWriterFunc
}

// ServeHTTP the http handler
func (h *LimitHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		h.handler.ServeHTTP(w, r)
		return
	}
	ctx := r.Context()
	var lv *LimitValue
	ctxTagValues := metadata.ExtractIncoming(ctx)
	if len(ctxTagValues) > 0 {
		lv = h.limiter.Config.MatchHeader(r.URL.Path, ctxTagValues)
	} else {
		lv = h.limiter.Config.MatchHeader(r.URL.Path, r.Header)
	}
	if lv.Quota == NoLimit {
		h.handler.ServeHTTP(w, r)
		return
	}
	if lv.Quota == Block {
		h.responseWriter(w, r, true, lv.Message)
		return
	}
	remaining, reset, allowed := h.limiter.PersistenceFn(r.Context(), lv.Key, lv.Quota, lv.Duration, 1)
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(lv.Quota))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.Itoa(int(reset/time.Second)))
	w.Header().Set("X-RateLimit-Resource", resourceKey(lv.Key))
	if !allowed {
		h.responseWriter(w, r, false, lv.Message)
		return
	}
	h.handler.ServeHTTP(w, r)
}

func resourceKey(src string) string {
	src = strings.ReplaceAll(src, "/", "")
	src = strings.ReplaceAll(src, ".", "")
	src = strings.ReplaceAll(src, "-", "")
	return src
}
