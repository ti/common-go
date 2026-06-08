package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

func handleRequestAndRedirect(res http.ResponseWriter, req *http.Request) {
	// Resolve the target server URL.
	target, err := url.Parse(req.RequestURI)
	if err != nil {
		http.Error(res, err.Error(), http.StatusBadRequest)
		return
	}

	// Build a new request used to forward to the target server.
	outReq := new(http.Request)
	*outReq = *req // copy the request content

	outReq.URL = target
	outReq.URL.Scheme = req.URL.Scheme
	outReq.URL.Host = req.URL.Host

	// Send the request.
	resp, err := http.DefaultTransport.RoundTrip(outReq)
	if err != nil {
		http.Error(res, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Copy the headers returned by the target server into the response.
	for key, value := range resp.Header {
		for _, v := range value {
			res.Header().Add(key, v)
		}
	}

	// Set the response status code.
	res.WriteHeader(resp.StatusCode)

	// Copy the body returned by the target server into the response.
	if _, err = io.Copy(res, resp.Body); err != nil {
		slog.Error("proxy copy response failed", "error", err)
	}
}

func main() {
	// Configure the listening port.
	http.HandleFunc("/", handleRequestAndRedirect)
	server := &http.Server{
		Addr:              "127.0.0.1:1180",
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		slog.Error("ListenAndServe failed", "error", err)
	}
}
