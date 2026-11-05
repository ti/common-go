// Package http provide http utils
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/ti/common-go/log"
)

// HTTP the http dep.
type HTTP struct {
	resolve               Resolver
	client                *http.Client
	metrics               *clientMetrics
	otelTracer            trace.Tracer
	protoJSONMarshaller   *protojson.MarshalOptions
	protoJSONUnmarshaller *protojson.UnmarshalOptions
	uri                   *url.URL
	base                  string
	proxy                 string
	path                  string
	resolverHostPath      string
	addr                  string
	tryTimes              int
	backoff               time.Duration
	retryNonIdempotent    bool
	tracing               bool
	log                   bool
	logBody               bool
	hasResolver           bool
	noMetrics             bool
	bufSize               int

	// baseTransport is the client's own *http.Transport (before tracing
	// wrapping), used to derive per-proxy transports. Nil when a custom
	// non-*http.Transport RoundTripper was supplied.
	baseTransport *http.Transport
	proxyClients  sync.Map // proxy URL string -> *http.Client
}

var resolvers = map[string]Resolver{}

// Resolver for the host.
type Resolver func(ctx context.Context, host string) (addr string, err error)

// RegisterResolver register the host.
func RegisterResolver(scheme string, resolver Resolver) {
	resolvers[scheme] = resolver
}

// New http client with uri, exp: New(ctx, "http://demo.test.com?try=23324")
func New(ctx context.Context, uri string, opts ...Option) (client *HTTP, err error) {
	o := evaluateOptions(opts)
	var u *url.URL
	u, err = url.Parse(uri)
	if err != nil {
		return nil, err
	}
	h := &HTTP{
		client: o.client,
	}
	err = h.Init(ctx, u)
	if err != nil {
		return nil, err
	}
	return h, nil
}

// Init configures the client from the URI. Query parameters:
//
//	timeout=5s              per-request timeout (default 10s)
//	try=N                   retry up to N times (see Request for which requests are retried)
//	backoff=100ms           base retry backoff; doubles per attempt with jitter, capped at max(2s, backoff)
//	retryNonIdempotent=true also retry POST/PATCH after the request may have reached the server
//	proxy=http://host:port  send this client's requests through a proxy
//	maxIdlePerHost=64       idle connections kept per host
//	idleTimeout=90s         how long idle connections are kept
//	h2c=true                HTTP/2 without TLS (prior knowledge) for http:// URLs
//	tracing=true            OpenTelemetry client spans
//	log=true, logBody=true  access log (with bodies)
//	metrics=false           disable Prometheus client metrics
//	emitUnpopulated=true    include zero values when sending proto messages
//	bufSize=N               Download buffer size
//
// A client supplied with WithHTTPClient is copied, never modified.
func (h *HTTP) Init(ctx context.Context, u *url.URL) error {
	query := u.Query()
	const trueStr = "true"
	h.uri = u
	h.tracing = query.Get("tracing") == trueStr
	h.log = query.Get("log") == trueStr
	h.logBody = query.Get("logBody") == trueStr
	if h.logBody {
		h.log = true
	}
	h.proxy = query.Get("proxy")
	h.tryTimes, _ = strconv.Atoi(query.Get("try"))
	h.retryNonIdempotent = query.Get("retryNonIdempotent") == trueStr
	h.backoff = 100 * time.Millisecond
	if d, err := time.ParseDuration(query.Get("backoff")); err == nil && d > 0 {
		h.backoff = d
	}
	h.bufSize, _ = strconv.Atoi(query.Get("bufSize"))
	if h.bufSize == 0 {
		h.bufSize = 96 * 1024
	}
	timeout, _ := time.ParseDuration(query.Get("timeout"))

	var proxyURL *url.URL
	if h.proxy != "" {
		var err error
		if proxyURL, err = url.Parse(h.proxy); err != nil {
			return fmt.Errorf("parse proxy %q: %w", h.proxy, err)
		}
	}
	if err := h.initClient(query, timeout, proxyURL); err != nil {
		return err
	}

	h.protoJSONMarshaller = &protojson.MarshalOptions{
		UseProtoNames: true,
	}
	if query.Get("emitUnpopulated") == trueStr {
		h.protoJSONMarshaller.EmitUnpopulated = true
	}
	h.noMetrics = query.Get("metrics") == "false"
	if !h.noMetrics {
		h.metrics = defaultClientMetrics
	}
	h.protoJSONUnmarshaller = &protojson.UnmarshalOptions{DiscardUnknown: true}
	if len(u.Path) > 0 {
		h.path = u.Path
	}
	h.base = fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	if h.tracing {
		h.otelTracer = otel.Tracer("client/" + u.Host)
	}
	if !internalSheme[u.Scheme] {
		resolver, ok := resolvers[u.Scheme]
		if !ok {
			return errors.New("can not find registered http resolver for " + u.Scheme)
		}
		h.hasResolver = true
		h.resolve = resolver
		var err error
		h.resolverHostPath, err = getResolverHostPath(u)
		if err != nil {
			return err
		}
		h.path = ""
		h.addr = fmt.Sprintf("%s://%s", u.Scheme, h.resolverHostPath)
	}
	return nil
}

// initClient builds the client: either a fresh one with a tuned transport, or
// a copy of the one supplied with WithHTTPClient.
func (h *HTTP) initClient(query url.Values, timeout time.Duration, proxyURL *url.URL) error {
	var c http.Client
	if h.client != nil {
		c = *h.client // copy: never mutate the caller's client
	} else {
		c.Timeout = 10 * time.Second
		c.Transport = newTransport(query)
	}
	if timeout > 0 {
		c.Timeout = timeout
	}
	switch t := c.Transport.(type) {
	case nil:
		h.baseTransport = cloneDefaultTransport()
	case *http.Transport:
		h.baseTransport = t
	}
	if proxyURL != nil {
		if h.baseTransport == nil {
			return errors.New("proxy= needs an *http.Transport; the supplied client uses " +
				fmt.Sprintf("%T", c.Transport))
		}
		h.baseTransport = h.baseTransport.Clone()
		h.baseTransport.Proxy = http.ProxyURL(proxyURL)
	}
	rt := c.Transport
	if h.baseTransport != nil {
		rt = h.baseTransport
	}
	if h.tracing {
		rt = otelhttp.NewTransport(rt)
	}
	c.Transport = rt
	h.client = &c
	return nil
}

// cloneDefaultTransport returns a private copy of http.DefaultTransport's
// settings (proxy from environment, dial and TLS timeouts, HTTP/2).
func cloneDefaultTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// newTransport returns a transport tuned for service-to-service traffic.
// http.DefaultTransport keeps only 2 idle connections per host, which forces
// a new TCP (and TLS) handshake for most concurrent requests.
func newTransport(query url.Values) *http.Transport {
	t := cloneDefaultTransport()
	t.MaxIdleConns = 512
	t.MaxIdleConnsPerHost = 64
	t.IdleConnTimeout = 90 * time.Second
	if n, err := strconv.Atoi(query.Get("maxIdlePerHost")); err == nil && n > 0 {
		t.MaxIdleConnsPerHost = n
	}
	if d, err := time.ParseDuration(query.Get("idleTimeout")); err == nil && d > 0 {
		t.IdleConnTimeout = d
	}
	if query.Get("h2c") == "true" {
		p := &http.Protocols{}
		p.SetUnencryptedHTTP2(true) // http:// → HTTP/2 with prior knowledge
		p.SetHTTP2(true)            // https:// → HTTP/2 over TLS
		t.Protocols = p
	}
	return t
}

// clientFor returns the client to use for ctx: the per-proxy client when the
// context carries a proxy (WithProxy), otherwise the main client.
func (h *HTTP) clientFor(ctx context.Context) *http.Client {
	proxyURL, ok := ProxyFromContext(ctx)
	if !ok {
		return h.client
	}
	key := proxyURL.String()
	if c, ok := h.proxyClients.Load(key); ok {
		return c.(*http.Client)
	}
	base := h.baseTransport
	if base == nil {
		base = cloneDefaultTransport()
	}
	tr := base.Clone()
	tr.Proxy = http.ProxyURL(proxyURL)
	var rt http.RoundTripper = tr
	if h.tracing {
		rt = otelhttp.NewTransport(tr)
	}
	c := &http.Client{
		Transport:     rt,
		Timeout:       h.client.Timeout,
		CheckRedirect: h.client.CheckRedirect,
		Jar:           h.client.Jar,
	}
	actual, _ := h.proxyClients.LoadOrStore(key, c)
	return actual.(*http.Client)
}

func getResolverHostPath(u *url.URL) (string, error) {
	if u.Host == "" {
		if u.Path == "" {
			return "", errors.New("can not find url path for " + u.Scheme)
		}
		return u.Path[1:], nil
	}
	resolverHostPath := u.Host
	if len(u.Path) > 1 {
		resolverHostPath += u.Path
	}
	return resolverHostPath, nil
}

var internalSheme = map[string]bool{
	"http":  true,
	"https": true,
	"host":  true,
	"hosts": true,
	"proxy": true,
}

// Request automatically serializes and deserializes requests
// Request and response support struct, string and []byte automatic conversion in three ways
// If the request is: struct, the request will be serialized into json automatically
// Among them, Path will be automatically added to the initialization to the route.
func (h *HTTP) Request(ctx context.Context, method,
	path string, header http.Header, reqData any,
	respDataPtr any,
) (err error) {
	if header == nil {
		header = http.Header{}
	}
	start := time.Now()
	base, reqBody, errBuildBody := h.buildRequestBody(ctx, path, header, reqData)
	if errBuildBody != nil {
		return errBuildBody
	}
	var tryTimes int
	var respBody []byte
	var statusCode int
	var httpRequestURL string
	httpPath := path
	if strings.HasPrefix(path, "/") {
		httpRequestURL = base + path
	} else if strings.Contains(path, "://") {
		httpRequestURL, err = h.getRequestURI(ctx, header, path)
		if err != nil {
			return err
		}
		httpPath = path
	} else {
		httpRequestURL = base + h.uri.Path + path
	}
	defer func() {
		h.onRequestClose(ctx, method, httpPath,
			tryTimes, start, reqBody, respBody, statusCode, err)
	}()
	if h.tracing {
		var span trace.Span
		ctx, span = h.otelTracer.Start(ctx, path)
		defer span.End()
	}
	var resp *http.Response
	safeToRetry := h.retryNonIdempotent || isIdempotent(method) || header.Get("Idempotency-Key") != ""
	for attempt := 0; ; attempt++ {
		resp, err = h.request(ctx, method, httpRequestURL, header, reqBody)
		tryTimes++
		if attempt >= h.tryTimes || !shouldRetry(resp, err, safeToRetry) {
			break
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // let the connection be reused
			_ = resp.Body.Close()
		}
		if waitErr := sleepCtx(ctx, retryDelay(h.backoff, attempt)); waitErr != nil {
			return waitErr
		}
	}
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	statusCode = resp.StatusCode
	respBody, err = io.ReadAll(resp.Body)
	if respDataPtr != nil {
		switch v := respDataPtr.(type) {
		case *string:
			*v = string(respBody)
		case *[]byte:
			*v = respBody
		default:
			if _, ok := respDataPtr.(proto.Message); ok {
				err = h.protoJSONUnmarshaller.Unmarshal(respBody, respDataPtr.(proto.Message))
			} else {
				err = json.Unmarshal(respBody, respDataPtr)
			}
			if err != nil {
				err = fmt.Errorf(" can not unmarshal %s to %s for %w ", string(respBody), reflect.TypeOf(respDataPtr), err)
			}
		}
	}
	if err != nil {
		return err
	}
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		return nil
	}
	return status.Error(codes.Code(statusCode), "http status code is not 200")
}

func (h *HTTP) getRequestURI(ctx context.Context, header http.Header, path string) (string, error) {
	requestURI, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	_ = header
	requestURI, err = resolveURL(ctx, requestURI)
	if err != nil {
		return "", err
	}
	return requestURI.String(), nil
}

// Download the file
func (h *HTTP) Download(ctx context.Context, downloadURL string, writer io.Writer) (written int64, err error) {
	if strings.HasPrefix(downloadURL, "/") {
		base := h.base
		if h.hasResolver {
			host, errResolve := h.resolve(ctx, h.resolverHostPath)
			if errResolve != nil {
				return 0, errResolve
			}
			base = "http://" + host + h.path
		}
		downloadURL = base + downloadURL
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	resp, errGet := h.clientFor(ctx).Do(req)
	if errGet != nil {
		return 0, errGet
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, status.Errorf(codes.Code(resp.StatusCode), "http status code is %d", resp.StatusCode)
	}
	buf := make([]byte, h.bufSize)
	return io.CopyBuffer(writer, resp.Body, buf)
}

func (h *HTTP) buildRequestBody(ctx context.Context, path string,
	header http.Header, reqData any,
) (base string, reqBody []byte, err error) {
	base = h.base
	if h.hasResolver && !strings.Contains(path, "://") {
		host, errResolve := h.resolve(ctx, h.resolverHostPath)
		if errResolve != nil {
			return base, nil, errResolve
		}
		base = "http://" + host + h.path
	}
	if reqData == nil {
		return
	}
	switch v := reqData.(type) {
	case []byte:
		reqBody = v
	case string:
		reqBody = []byte(v)
	default:
		if protoData, ok := reqData.(proto.Message); ok {
			reqBody, err = h.protoJSONMarshaller.Marshal(protoData)
		} else {
			reqBody, err = json.Marshal(reqData)
		}
		if err == nil {
			header.Set("Content-Type", "application/json")
		}
	}
	return
}

func resolveURL(ctx context.Context, u *url.URL) (*url.URL, error) {
	resolverHostPath, err := getResolverHostPath(u)
	if err != nil {
		return nil, err
	}
	if resolve, ok := resolvers[u.Scheme]; ok {
		host, errResolve := resolve(ctx, resolverHostPath)
		if errResolve != nil {
			return nil, errResolve
		}
		u.Host = host
		u.Scheme = "http"
		return u, nil
	}
	return u, nil
}

func (h *HTTP) onRequestClose(ctx context.Context,
	method, path string, tryTimes int, start time.Time,
	reqBody, respBody []byte, statusCode int, err error,
) {
	used := time.Since(start)
	if h.log {
		logData := map[string]any{
			"status":   statusCode,
			"referer":  tryTimes,
			"protocol": "http/client",
			"duration": durationToMilliseconds(used),
		}
		if i := strings.Index(path, "?"); i > 1 {
			logData["action"] = method + ":" + path[0:i]
			logData["params"] = path[i+1:]
		} else {
			logData["action"] = method + ":" + path
		}
		if h.addr != "" {
			logData["host"] = h.addr
		}
		if h.logBody {
			if len(reqBody) > 2048 {
				reqBody = reqBody[0:2048]
			}
			if len(respBody) > 2048 {
				respBody = respBody[0:2048]
			}
			logData["request"] = string(reqBody)
			logData["response"] = string(respBody)
		}
		logger := log.Extract(ctx).With(logData)
		if err != nil {
			logger.Warn(err.Error())
		} else {
			logger.Info(method)
		}
	}
	if h.metrics != nil {
		// Query strings are unbounded label values; keep only the path.
		metricPath, _, _ := strings.Cut(path, "?")
		h.metrics.clientHandledCounter.WithLabelValues(h.uri.Scheme, h.uri.Host, metricPath, strconv.Itoa(statusCode)).Inc()
		h.metrics.clientHandledHistogram.WithLabelValues(h.uri.Scheme, h.uri.Host, metricPath).Observe(used.Seconds())
	}
}

func (h *HTTP) request(ctx context.Context, method, uri string,
	header http.Header, reqBody []byte,
) (*http.Response, error) {
	var request *http.Request
	var err error
	if len(reqBody) > 0 {
		request, err = http.NewRequestWithContext(ctx, method, uri, bytes.NewReader(reqBody))
	} else {
		request, err = http.NewRequestWithContext(ctx, method, uri, nil)
	}
	if host := header.Get("host"); host != "" {
		request.Host = host
	}
	if err != nil {
		return nil, err
	}
	request.Header = header
	return h.clientFor(ctx).Do(request)
}

// Close releases idle connections of this client and its per-proxy clients.
func (h *HTTP) Close(_ context.Context) error {
	h.client.CloseIdleConnections()
	h.proxyClients.Range(func(_, c any) bool {
		c.(*http.Client).CloseIdleConnections()
		return true
	})
	return nil
}

// isIdempotent reports whether a request with this method can be repeated
// safely (RFC 9110 §9.2.2).
func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace,
		http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// shouldRetry decides whether an attempt is worth repeating. Connection
// failures before the request was sent (dial errors) are always retried;
// other errors and 502/503/504 responses only when the request is safe to
// repeat.
func shouldRetry(resp *http.Response, err error, safeToRetry bool) bool {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		if opErr, ok := errors.AsType[*net.OpError](err); ok && opErr.Op == "dial" {
			return true
		}
		return safeToRetry
	}
	switch resp.StatusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return safeToRetry
	}
	return false
}

// retryDelay is exponential backoff with jitter: base·2^attempt, capped at
// max(2s, base), randomized to the upper half so retries don't synchronize.
func retryDelay(base time.Duration, attempt int) time.Duration {
	limit := max(2*time.Second, base)
	d := base << min(attempt, 16)
	if d <= 0 || d > limit {
		d = limit
	}
	half := d / 2
	return half + rand.N(half+1)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Resolve return one of the addr
func (h *HTTP) Resolve(ctx context.Context) (addr string, err error) {
	if h.hasResolver {
		return h.resolve(ctx, h.resolverHostPath)
	}
	return h.addr, nil
}

// SetTransport replaces the client's transport (wrapped for tracing when
// tracing=true).
func (h *HTTP) SetTransport(transport http.RoundTripper) {
	if t, ok := transport.(*http.Transport); ok {
		h.baseTransport = t
	}
	if h.tracing {
		h.client.Transport = otelhttp.NewTransport(transport)
	} else {
		h.client.Transport = transport
	}
}

// Client the http client
func (h *HTTP) Client() *http.Client {
	return h.client
}

// String the http uri
func (h *HTTP) String() string {
	return h.uri.String()
}

func durationToMilliseconds(duration time.Duration) float32 {
	milliseconds := float32(duration.Nanoseconds()/1000) / 1000
	return milliseconds
}
