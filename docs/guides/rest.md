# REST

A method that carries a `google.api.http` annotation can be served as a REST route on the HTTP port, next to gRPC and Connect, from the same implementation. gRPC and Connect stay the core protocols; REST is for callers that want resource URLs, plain `curl`, or an OpenAPI document.

```proto
import "google/api/annotations.proto";

service NoteService {
  rpc GetNote(GetNoteRequest) returns (GetNoteResponse) {
    option (google.api.http) = {get: "/v1/notes/{id}"};
  }
}
```

```go
gs := grpcmux.NewServer(grpcmux.WithConfig(&cfg.Apis), grpcmux.WithREST()) // or apis.rest: true
notev1.RegisterNoteServiceServer(gs, impl)                                  // GET /v1/notes/{id} is live
```

That is the whole setup. `WithREST()` (config key `rest`) makes every service registration (`pb.RegisterXxxServer(gs, impl)`, `grpcmux.RegisterConnectService`) also read the service's protobuf descriptors and add a route per annotation. There is no generated gateway code, no per-service REST call and no hand-written route. Methods without an annotation are untouched, and a server without `WithREST` behaves exactly as before.

What you need to generate is what gRPC needs anyway: `protoc-gen-go` and `protoc-gen-go-grpc` (plus `buf.build/googleapis/googleapis` as a buf dependency for `google/api/annotations.proto`). The annotations are compiled into the Go descriptors, which is where grpcmux reads them.

## A REST call is the same call

| Concern | On a REST route |
|---|---|
| HTTP middleware | CORS, request ID, access log, authentication, rate limiting: the same middleware as every route on the HTTP port |
| Interceptors | The chain of the HTTP bridges ([server.md](server.md#interceptors)): authz policy, protovalidate / `Validate()`, `grpc_server_*` metrics, deadlines ([rate-limiting.md](rate-limiting.md#server-side-deadlines)), your unary and stream interceptors, panic recovery. `grpc.Method(ctx)` and `info.FullMethod` are the RPC's full method. `WithoutHTTPInterceptors()` turns it off here too |
| Auth exemptions | By RPC name. `WithNoAuthMethods("/note.v1.NoteService/GetNote")` exempts the method on gRPC, Connect **and** its REST route; you do not list REST paths. A path-based exemption (`WithNoAuthPrefixes("/v1/public/")`) works as well |
| Rate limits, deadlines | Rules match the RPC's full method, so one rule covers every protocol |
| Access log | `action` is the full method (`/note.v1.NoteService/GetNote`), never the path with its variables, so it is the same low-cardinality value on every protocol |
| Handler context | Incoming metadata from the request headers, the peer address, `grpc.SetHeader` / `SetTrailer` (forwarded as response headers when the key starts with `x-` or is a standard header) |

## Mapping

The rules are those of [`google.api.http`](https://github.com/googleapis/googleapis/blob/master/google/api/http.proto), implemented with grpc-gateway's runtime (path templates, query parsing, field masks), so behavior matches what `protoc-gen-grpc-gateway` generates.

| Rule | Behavior |
|---|---|
| `get` / `post` / `put` / `patch` / `delete` / `custom` | The HTTP method and path template. Templates support `{field}`, nested `{user.user_id}`, multi-segment `{name=shelves/*}` and `{name=**}`, and custom verbs (`/v1/users:export`) |
| Path variables | Bound to the named request field, URL-unescaped. A value of the wrong type is `400 invalid_argument` |
| `body: "*"` | The JSON body is the whole request message. Path variables are applied after it and win; there are no query parameters |
| `body: "note"` | The JSON body is that field; the other fields come from the path and the query |
| No `body` | The request body is ignored |
| Query parameters | Every request field not bound by the path or the body, by proto name or JSON name (`page_size` or `pageSize`), nested with dots (`filter.label=x`), repeated by repeating the key, maps as `labels[k]=v`. Well-known types work: `update_mask=title,body`, timestamps as RFC 3339. Unknown parameters are ignored; a value of the wrong type, or the same field given twice, is `400` |
| `PATCH` with `body: "<message field>"` | When the request has exactly one `google.protobuf.FieldMask` field and it is empty, it is set to the fields present in the JSON body (AIP-134). An explicit `?update_mask=` wins |
| `response_body: "note"` | The response is that field instead of the whole response message |
| `additional_bindings` | One route per binding |

JSON follows the server's naming setting ([json-format.md](json-format.md)): snake_case by default, camelCase with `useCamelCase`; input accepts both. As everywhere in protobuf JSON, 64-bit integers are strings, enums are their names, `google.protobuf.Timestamp` is an RFC 3339 string and unset fields are omitted. A response of type `google.api.HttpBody` is sent raw with its content type.

```bash
curl -X POST localhost:8080/v1/users -H 'Content-Type: application/json' \
  -d '{"name":"Ada","email":"ada@example.com"}'                    # body: "user"
curl 'localhost:8080/v1/users?page_size=2&page_token=...'           # query parameters
curl -X PATCH localhost:8080/v1/users/42 -d '{"name":"Ada L."}'     # mask = fields in the body
curl -X PATCH 'localhost:8080/v1/users/42?update_mask=email' -d '{"email":"a@b.c"}'
curl -X DELETE localhost:8080/v1/users/42
```

## Errors

A failed call answers with the HTTP status of its gRPC code and the JSON error body used by the rest of the HTTP port:

```json
{
  "error": "already_exists",
  "error_code": 6,
  "error_description": "email ada@example.com is already in use",
  "details": [
    {"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "EMAIL_ALREADY_IN_USE", "domain": "example.user.v1", "metadata": {"email": "ada@example.com"}}
  ]
}
```

`WithREST` turns on **typed details** for the HTTP port of the server (`WithTypedErrorDetails()` / `typedErrorDetails: true` does the same without REST): `details` are the status details as protobuf JSON with their `@type`: the same messages a Connect client finds in the `details` of the Connect error (there as `type` + base64 `value`) and a gRPC client in the status. Branch on `error` (the code) and on the `reason` / `domain` of the `google.rpc.ErrorInfo` detail, not on `error_description`. Validation failures carry a `buf.validate.Violations` detail (`violations[].rule_id`, `field`, `message`); a rate-limited call carries `google.rpc.RetryInfo` and `ErrorInfo` ([rate-limiting.md](rate-limiting.md#what-the-client-sees)). A detail whose message type is not linked into the server binary cannot be rendered and is left out.

Without typed details (the default of a server that does not use `WithREST`) each entry is the bare message, `{"reason":"...","domain":"..."}`, and a detail with no set fields is omitted. That is the format this library has always written and existing APIs keep it unless they opt in. Two things follow for a service that adds `WithREST` to an API with published clients: the bodies of its existing gateway routes and middleware rejections gain `@type` too (the option is per server, not per route), and errors a handler writes itself with `mux.WriteHTTPErrorResponse` stay untyped until `mux.SetDefaultOptions(mux.WithTypedErrorDetails())` is called.

| gRPC code | HTTP | gRPC code | HTTP |
|---|---|---|---|
| `INVALID_ARGUMENT`, `FAILED_PRECONDITION`, `OUT_OF_RANGE` | 400 | `RESOURCE_EXHAUSTED` | 429 |
| `UNAUTHENTICATED` | 401 | `CANCELED` | 499 |
| `PERMISSION_DENIED` | 403 | `UNKNOWN`, `INTERNAL`, `DATA_LOSS` | 500 |
| `NOT_FOUND` | 404 | `UNIMPLEMENTED` | 501 |
| `ALREADY_EXISTS`, `ABORTED` | 409 | `UNAVAILABLE` | 503 |
| | | `DEADLINE_EXCEEDED` | 504 |

Codes registered with `mux.RegisterErrorCodes` keep their names and statuses. Malformed JSON is `400 invalid_argument`. A path no route matches is `404` with `"error": "not_found"` and the description `API NOT FOUND`; a REST path called with an HTTP method it is not bound to goes to the other routes of the server first (a `gs.HandleFunc` route for that method, generated gateway routes, a catch-all), and only if none takes it is it `501` with `"error": "unimplemented"` (the answer of grpc-gateway routes on this server). `HEAD` is routed as the `GET` it mirrors: the same status and headers without a body, `404` for an unrouted path, and behind authentication like the `GET`.

> With `WithREST` an unrouted path answers **HTTP 404**. Without it the same body is sent with HTTP 200, as this server has always done; that will become 404 for everyone in a later release (see the [upgrade note](../upgrades/rest-and-rate-limiting.md)). Do not point a health check at an unrouted path such as `/`; use `/healthz` and `/readyz`.

## Streaming

A **server-streaming** method with an annotation answers with newline-delimited JSON, one object per message, flushed as it is sent:

```
$ curl localhost:8080/v1/users:export
{"result":{"user":{"user_id":"3","name":"c"}}}
{"result":{"user":{"user_id":"2","name":"b"}}}
```

- An error **before the first message** (validation, not found, authentication, rate limit) is an ordinary error response: HTTP status and the error body above.
- An error **after** it cannot change the status any more (200 is on the wire). The stream ends with a last line `{"error":{"code":10,"message":"...","details":[...]}}`, a `google.rpc.Status`. A client must read to the end and check the last line.
- Response headers set with `SetHeader` before the first message are HTTP response headers.
- When the client disconnects, the handler's context is canceled. The stream runs the stream interceptor chain, counts once for rate limiting (when it is opened), is never given a server-side deadline, and ends with an `UNAVAILABLE` error line when the server drains with `WithDrainStreams`.

For a browser, prefer Connect server streaming (typed messages, typed errors, [connectrpc.md](connectrpc.md)); the REST form is for `curl`, scripts and clients that only speak HTTP+JSON.

**Client-streaming and bidirectional** methods have no REST form. An annotation on one is ignored (a warning is logged at registration).

## CORS

REST routes are behind the server's CORS handling like every HTTP route: `GET, POST, PUT, PATCH, DELETE, OPTIONS` are allowed for the configured origins, and preflights are answered by the middleware ([server.md](server.md)).

## OpenAPI

The OpenAPI document is generated from the same annotations with the grpc-gateway `openapiv2` buf plugin, pinned to the version of the grpc-gateway runtime in `go.mod`. From [`examples/buf.gen.yaml`](../../examples/buf.gen.yaml):

```yaml
plugins:
  - remote: buf.build/grpc-ecosystem/openapiv2:v2.33.0
    out: gen/openapi
    opt:
      - allow_merge=true              # one document for all services
      - merge_file_name=api           # gen/openapi/api.swagger.json
      - json_names_for_fields=false   # snake_case, the server's default JSON naming
```

```bash
cd examples && buf generate          # writes gen/openapi/api.swagger.json
```

- Every plugin in that file is pinned to an exact version, so the output only changes when a pin or a proto changes. Running `buf generate` twice on an unchanged tree produces identical files; commit the result and let CI fail on a diff.
- `json_names_for_fields=false` documents `user_id`, which is what the server sends by default. Remove the option for a server that runs with `useCamelCase: true`, or the document and the wire disagree.
- The document describes paths, parameters and message schemas. Error responses are documented as `google.rpc.Status`, which is **not** this server's error body (see [Errors](#errors)); describe the real one in your API documentation, or post-process the document.
- The plugin emits OpenAPI v2 (Swagger). grpc-gateway's `protoc-gen-openapiv3` exists but is marked alpha as of v2.33.0, so it is not used here. Convert v2 to v3 downstream if a tool needs it.
- `examples/internal/app/rest_test.go` (`TestOpenAPIMatchesServedRoutes`) checks that every documented operation is actually routed.

## Routing through a gateway

REST paths do not have the `/package.Service/Method` shape of gRPC and Connect, so an ingress rule that only matches that shape will not forward them. Give REST a prefix of its own (the examples use `/v1/`) and add it to the route that points at the HTTP port:

```yaml
rules:
  - matches:
      - path: {type: RegularExpression, value: "^/[a-z]+\\.v[0-9]+[a-z]*\\..+"}   # Connect / gRPC-Web
      - path: {type: PathPrefix, value: /v1/}                                      # REST
    backendRefs: [{name: app, port: 8080}]
```

A frontend served from the same host must not own that prefix.

## What it does not do

- Client-streaming and bidirectional methods (no REST mapping exists).
- `google.api.HttpBody` as a **request** body (raw uploads) and multipart forms: register a handler with `gs.Handle` for those.
- HTTP rules supplied outside the proto file (the `http:` section of a gRPC API service configuration YAML): only annotations are read.
- `application/x-www-form-urlencoded` bodies and `Content-Encoding: gzip` request bodies.
- WebSockets (`gs.HandleWebSocket`).

## Other ways to serve REST, and why this one

| | `WithREST` | Generated gateway (`protoc-gen-grpc-gateway`) | [Vanguard](https://github.com/connectrpc/vanguard-go) v0.5 |
|---|---|---|---|
| Generated code per service | none | `*.pb.gw.go`, plus a `RegisterXxxHandler...` call per service | none |
| Interceptor chain | the HTTP bridges' chain, in process | none with `RegisterXxxHandlerServer`; the full gRPC chain (auth, logging and rate limiting run a second time) through `gs.Conn()` | the connect-go server's interceptors |
| Server streaming of ordinary messages | NDJSON | NDJSON (through `gs.Conn()` only) | not supported: only `google.api.HttpBody` streams; mounting a server that has an annotated message stream fails |
| Error body | this server's JSON error body, custom codes included | the same | `google.rpc.Status` JSON |
| Routing | per-template routes; other paths fall through to the server's own routes | the same mux | one catch-all handler on `/` |
| Dependencies | none new (grpc-gateway runtime is already a dependency) | none new | a second transcoder next to the grpc-gateway runtime, pre-1.0 |

Vanguard's model (transcode onto a `connect.Server`) is the closest to this library's Connect path and needs no generated code either; it was not adopted because it cannot serve the server-streaming methods the reference services have, would change the error body that existing REST clients parse, and would put two transcoders into every binary while `gs.ServeMux()` has to keep working. Revisit when it can stream ordinary messages.

### Transcoding at the gateway instead

Envoy Gateway (v1.10 and later) can transcode in front of the service: an `HTTPRouteFilter` with `grpcJSONTranscoder` (API `gateway.envoyproxy.io/v1alpha1`) takes a descriptor set from a ConfigMap and turns REST requests into gRPC calls to the gRPC port. Its attraction is that it is the same for a backend in any language, with no code in the service.

| | In process (`WithREST`) | At the gateway |
|---|---|---|
| Works without Envoy Gateway >= 1.10 (local run, tests, other ingresses) | yes | no; contract tests need an Envoy |
| Authentication | everything the HTTP middleware accepts, cookie sessions included | what reaches the gRPC port as metadata: bearer tokens, not cookie sessions handled by HTTP middleware |
| Error body | this server's JSON error body, custom codes | `google.rpc.Status` JSON |
| `PATCH` without `update_mask` | mask derived from the body | no derivation: the mask is empty |
| Rate limits, access log, exemptions | by RPC name, in the service | the service sees an ordinary gRPC call from the gateway |
| Schema changes | deployed with the service | the descriptor ConfigMap must be rolled out in step with the service |

The two do not produce the same wire behavior (error body and PATCH above), so a product should pick one per API, not mix them. Recommendation: `WithREST` (and its counterpart in the other backends, see below) where REST is part of the product's public contract, sessions are cookies, or the service must run outside one specific gateway; gateway transcoding only for internal or bearer-only APIs whose clients accept `google.rpc.Status` errors. With gateway transcoding nothing in-process is needed beyond the gRPC port; `WithREST` stays off.

### The same behavior in other languages

A backend without a transcoder (Python, Rust) can match `WithREST` with a small layer in front of its existing Connect unary JSON endpoint: build the route table from the `google.api.http` rules in the descriptor set, merge path variables, query parameters and body into one protobuf-JSON request object, call the Connect handler in process (so authentication, validation and metrics run unchanged) and translate the result. The rules such a layer has to reproduce are listed case by case in the [upgrade note, section 4](../upgrades/rest-and-rate-limiting.md#4-observable-contract).

### Generated gateway code

Precedence between kinds of routes is fixed: handlers registered with `gs.Handle` (and the Connect procedures) are served first, then REST routes, then the gateway mux (`gs.HandleFunc`, generated gateway routes). A path that a `gs.Handle` pattern owns is never treated as the REST call a template would also match, so it does not inherit that RPC's auth exemption or rate-limit rule.

The generated gateway keeps working: routes registered on `gs.ServeMux()` with `pb.RegisterXxxHandlerServer` or `pb.RegisterXxxHandlerClient(ctx, gs.ServeMux(), pb.NewXxxClient(gs.Conn()))` are served as before, also next to `WithREST`. Do not register the same service both ways; the `WithREST` route would win. `gs.HandleFunc` and `WithAutoHTTP` are unchanged as well.
