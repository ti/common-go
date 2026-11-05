# ConnectRPC

[Connect](https://connectrpc.com) is an HTTP protocol for protobuf RPCs that works with plain `fetch`/`curl` and with generated clients (connect-web, connect-go, connect-kotlin, connect-swift). grpcmux serves it on the HTTP port in two ways.

## `RegisterConnectService`: unary methods, no extra codegen

```go
grpcmux.RegisterConnectService(gs, &pb.UserService_ServiceDesc, impl)
```

This registers the service on the gRPC server and mounts every **unary** method at `POST /<package.Service>/<Method>` on the HTTP port. It needs only the standard `protoc-gen-go` + `protoc-gen-go-grpc` output. It implements the Connect unary protocol:

| Feature | Behavior |
|---|---|
| Encodings | `application/proto` (binary) and `application/json`. A missing or non-Connect content type is read as JSON, so `curl -d '{...}'` works. `application/connect+*` and `application/grpc*` (streaming framing) get `415`. |
| Compression | gzip request bodies (`Content-Encoding: gzip`); responses of 1 KiB or more are gzipped when the client sends `Accept-Encoding: gzip` |
| GET | `GET /pkg.Service/Method?encoding=json&message=<url-encoded JSON>` (or `encoding=proto&base64=1&message=<base64url>`, optional `compression=gzip`) for methods declared `option idempotency_level = NO_SIDE_EFFECTS;`. GET responses are cacheable by CDNs and browsers. A GET to any other method fails with `unimplemented` (HTTP 501); methods other than GET and POST get `405`. |
| Deadlines | `Connect-Timeout-Ms` becomes the handler's context deadline; expiring returns `deadline_exceeded` |
| Protocol version | `Connect-Protocol-Version` other than `1` is rejected |
| Errors | JSON `{"code":"not_found","message":"...","details":[{"type":"google.rpc.ErrorInfo","value":"<base64>"}]}` with the matching HTTP status; codes registered with `mux.RegisterErrorCodes` keep their names and statuses |
| Metadata | Request headers arrive as incoming gRPC metadata; `grpc.SetHeader` values are response headers and `grpc.SetTrailer` values are `Trailer-` prefixed headers |
| Limits | Request bodies above `WithMaxRequestBodyBytes` (default 32 MiB) fail with `resource_exhausted` |
| JSON names | snake_case by default, camelCase with `useCamelCase` ([json-format.md](json-format.md)); input accepts both |
| Interceptors | The gRPC unary chain runs (validation, authz, metrics, custom interceptors, recovery; rate limiting comes from the routerlimit HTTP middleware); see [server.md](server.md#interceptors) |

The HTTP middleware (CORS, request ID, access log, auth) wraps these routes like every other HTTP route.

```bash
curl -X POST localhost:8080/example.user.v1.UserService/GetUser \
  -H 'Content-Type: application/json' -d '{"user_id":"42"}'
curl 'localhost:8080/example.user.v1.UserService/GetUser?encoding=json&message=%7B%22user_id%22%3A%2242%22%7D'
```

## connect-go v2 handlers (streaming, gRPC-Web)

`RegisterConnectService` bridges unary methods only. For server, client and bidirectional streaming over Connect, generate connect-go **v2** code (`buf.build/connectrpc/go` plugin; it imports `connectrpc.com/connect/v2`) and register it on the server's `connect.Server`:

```go
pb.RegisterUserServiceServer(gs, svc)                                // native gRPC
userv1connect.RegisterUserServiceHandler(gs.ConnectServer(), handler) // Connect, gRPC-Web, gRPC on :8080
```

`gs.ConnectServer()` is mounted on the HTTP port with `connecthttp` when the server starts, so its procedures speak Connect, gRPC-Web and gRPC behind the HTTP middleware (CORS, request ID, auth, access log). grpcmux adds two interceptors:

- unary calls run the grpcmux unary interceptor chain (validation, authz, metrics, custom interceptors, recovery);
- gRPC status errors returned by handlers become `*connect.Error` with the same code, message and details. connect-go v2 only sends messages of `*connect.Error` to clients, so without this a handler shared with the gRPC server would answer `unknown` with no message.

JSON follows the server's snake_case/camelCase setting, and request size is limited by `WithMaxRequestBodyBytes`. Register handlers before `Start`.

The connect-go v2 handler interface differs from the gRPC server interface only for streaming methods. A small adapter type lets one implementation serve both; see `userservice.ConnectHandler` in the [examples](../../examples/) module, which also serves `ExportUsers` over all three protocols.

A procedure path must be registered only once: if you use connect-go handlers for a service, don't also call `RegisterConnectService` for it. `RegisterConnectHandler(gs, pattern, h)` still mounts any `http.Handler`, such as connect-go v1 handlers, but those don't run the grpcmux interceptor chain.

## Browser clients (connect-web)

```ts
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { UserService } from "./gen/example/user/v1/user_pb";

const client = createClient(UserService, createConnectTransport({
  baseUrl: "https://api.example.com",
  useBinaryFormat: false,     // JSON is easiest to debug; true for smaller payloads
  useHttpGet: true,           // GET for NO_SIDE_EFFECTS methods
}));
const { user } = await client.getUser({ userId: "42" });
```

CORS: the default allows every origin and includes the Connect headers (`Connect-Protocol-Version`, `Connect-Timeout-Ms`). Restrict origins with `WithCORS`. See [examples/web/connect](../../examples/web/connect) for a Vite project.
