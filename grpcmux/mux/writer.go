package mux

import (
	"net/http"
)

type responseWriter struct {
	W                 http.ResponseWriter
	R                 *http.Request
	BodyReWriter      func(contentType, requestID string, body []byte) []byte
	WithoutHTTPStatus bool
	RequestID         string

	status            int
	withoutHTTPStatus bool
	written           bool
}

// Header implement responseWriter
func (l *responseWriter) Header() http.Header {
	return l.W.Header()
}

// Write implement responseWrite
func (l *responseWriter) Write(b []byte) (int, error) {
	if l.BodyReWriter != nil {
		contentType := l.Header().Get("content-type")
		b = l.BodyReWriter(contentType, l.RequestID, b)
		l.written = true
	}
	return l.W.Write(b)
}

// WriteHeader write header
func (l *responseWriter) WriteHeader(s int) {
	l.status = s
	if !l.withoutHTTPStatus {
		l.W.WriteHeader(s)
	}
}

// Flush sends any buffered data to the client.
func (l *responseWriter) Flush() {
	flusher, ok := l.W.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}
