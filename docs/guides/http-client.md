# HTTP client (`dependencies/http`)

`*dephttp.HTTP` is an HTTP client configured from a URI, with JSON/proto encoding, pooling, retries, proxies, access logs, metrics and tracing.

```go
import dephttp "github.com/ti/common-go/dependencies/http"

users, err := dephttp.New(ctx, "http://users.svc:8080/api?timeout=3s&try=2&log=true")

var out struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
err = users.Request(ctx, http.MethodGet, "/v1/users/42", nil, nil, &out)
```

As a dependency field: `Users *dephttp.HTTP` with `users: http://users.svc:8080?timeout=3s` ([dependencies.md](dependencies.md)).

## `Request(ctx, method, path, header, reqData, respDataPtr)`

- **URL**: a `path` starting with `/` is appended to `scheme://host` of the client URI; a full URL (`https://...`) is used as is; any other path is appended to the client URI including its path.
- **Request body** (`reqData`): `[]byte` and `string` are sent as is; a `proto.Message` is encoded with protojson (proto names; `emitUnpopulated=true` to include zero values); anything else is JSON-encoded with `encoding/json` and `Content-Type: application/json` is set.
- **Response** (`respDataPtr`): `*string` or `*[]byte` receive the raw body; a `proto.Message` is decoded with protojson (unknown fields ignored); anything else is decoded with `encoding/json` (field names matched case-insensitively, as usual for third-party JSON). `nil` discards the body.
- **Status**: a non-2xx response returns `status.Error(codes.Code(httpStatus), ...)`, so `status.Code(err)` is the HTTP status number.

`Download(ctx, url, w)` streams a GET response into a writer. `Client()` returns the underlying `*http.Client`.

## URI parameters

| Parameter | Default | Meaning |
|---|---|---|
| `timeout=5s` | `10s` | Whole-request timeout |
| `try=N` | `0` | Retry up to N times |
| `backoff=100ms` | `100ms` | Base retry delay; doubles per attempt with jitter, capped at max(2s, backoff) |
| `retryNonIdempotent=true` | off | Also retry POST/PATCH after the request may have reached the server |
| `proxy=http://host:port` | environment (`HTTPS_PROXY`, ...) | Proxy for this client |
| `maxIdlePerHost=N` | `64` | Idle connections kept per host |
| `idleTimeout=90s` | `90s` | How long idle connections are kept |
| `h2c=true` | off | HTTP/2 without TLS (prior knowledge) for `http://` URLs |
| `tracing=true` | off | OpenTelemetry client spans |
| `log=true`, `logBody=true` | off | Access log, with bodies (truncated to 2 KiB) |
| `metrics=false` | on | Prometheus `http_client_handled_total` and `http_client_handling_seconds` (labels: scheme, host, path without query, status) |
| `emitUnpopulated=true` | off | Include zero values in proto request bodies |
| `bufSize=N` | 96 KiB | `Download` buffer size |

## Retries

A retry happens when attempts remain and:

- the connection could not be established (dial error): always, because the request was never sent;
- another transport error, or a `502`, `503` or `504` response: only if the request is safe to repeat, meaning the method is idempotent (GET, HEAD, OPTIONS, TRACE, PUT, DELETE), or the request has an `Idempotency-Key` header, or `retryNonIdempotent=true`.

Retries wait with jittered exponential backoff and stop as soon as the context is done.

## Connection pooling and proxies

Each client owns a private transport tuned for service-to-service traffic (`http.DefaultTransport` keeps only 2 idle connections per host). Settings never leak into other clients.

`dephttp.WithProxy(ctx, "http://proxy:3128")` routes a single request through a proxy. The client keeps one pooled transport per proxy URL, so repeated requests reuse connections; `Close` releases them. Use it for a small, fixed set of proxies (each distinct proxy URL gets its own pool).

`dephttp.WithHTTPClient(c)` supplies your own `*http.Client`. It is copied, never modified; `proxy=` requires its transport to be an `*http.Transport` (or nil).

## Custom resolvers

`dephttp.RegisterResolver("consul", func(ctx context.Context, host string) (string, error) {...})` lets URIs like `consul://users/api` resolve an address per request.
