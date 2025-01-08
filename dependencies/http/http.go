// Package http provide http utils
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/ti/common-go/log"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
	tracing               bool
	log                   bool
	logBody               bool
	hasResolver           bool
	noMetrics             bool
	bufSize               int
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

// Init the http client with url params
func (h *HTTP) Init(ctx context.Context, u *url.URL) error {
	query := u.Query()
	if h.client == nil {
		h.client = &http.Client{
			Timeout: 10 * time.Second,
		}
		h.SetTransport(http.DefaultTransport)
	}
	h.uri = u
	if timeout, _ := time.ParseDuration(query.Get("timeout")); timeout > 0 {
		h.client.Timeout = timeout
	}
	h.protoJSONMarshaller = &protojson.MarshalOptions{
		UseProtoNames: true,
	}
	if query.Get("emitUnpopulated") == "true" {
		h.protoJSONMarshaller.EmitUnpopulated = true
	}
	h.noMetrics = query.Get("metrics") == "false"
	if !h.noMetrics {
		h.metrics = defaultClientMetrics
	}
	h.protoJSONUnmarshaller = &protojson.UnmarshalOptions{DiscardUnknown: true}
	h.tryTimes, _ = strconv.Atoi(query.Get("try"))
	h.bufSize, _ = strconv.Atoi(query.Get("bufSize"))
	const trueStr = "true"
	h.tracing = query.Get("tracing") == trueStr
	h.log = query.Get("log") == trueStr
	h.logBody = query.Get("logBody") == trueStr
	h.proxy = query.Get("proxy")
	if h.logBody {
		h.log = true
	}
	if len(u.Path) > 0 {
		h.path = u.Path
	}
	if h.bufSize == 0 {
		h.bufSize = 96 * 1024
	}
	h.base = fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	if h.tracing {
		h.otelTracer = otel.Tracer("client/" + u.Host)
	}
	scheme := u.Scheme

	if !internalSheme[scheme] {
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
	if h.proxy != "" {
		proxyURL, err := url.Parse(h.proxy)
		if err != nil {
			return errors.New("parse proxy error %s" + h.proxy)
		}
		if t, ok := h.client.Transport.(*http.Transport); ok {
			t.Proxy = func(request *http.Request) (*url.URL, error) {
				return proxyURL, nil
			}
		}
	}
	return nil
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
	for i := 0; i <= h.tryTimes; i++ {
		resp, err = h.request(ctx, method,
			httpRequestURL, header, reqBody)
		tryTimes++
		if err == nil {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
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
			base = fmt.Sprintf("http://" + host + h.path)
		}
		downloadURL = base + downloadURL
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	resp, errGet := h.client.Do(req)
	if errGet != nil {
		return 0, errGet
	}
	defer resp.Body.Close()
	if !(resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices) {
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
		base = fmt.Sprintf("http://" + host + h.path)
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
		h.metrics.clientHandledCounter.WithLabelValues(h.uri.Scheme, h.uri.Host, path, strconv.Itoa(statusCode)).Inc()
		h.metrics.clientHandledHistogram.WithLabelValues(h.uri.Scheme, h.uri.Host, path).Observe(used.Seconds())
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
	return h.client.Do(request)
}

// Close the http client
func (h *HTTP) Close(_ context.Context) error {
	h.client.CloseIdleConnections()
	return nil
}

// Resolve return one of the addr
func (h *HTTP) Resolve(ctx context.Context) (addr string, err error) {
	if h.hasResolver {
		return h.resolve(ctx, h.resolverHostPath)
	}
	return h.addr, nil
}

// SetTransport set the http transport
func (h *HTTP) SetTransport(transport http.RoundTripper) {
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
