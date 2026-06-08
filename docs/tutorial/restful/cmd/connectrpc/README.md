# ConnectRPC + gRPC-Gateway + gRPC

Built on top of the `camelCase` example, this adds ConnectRPC protocol support. A single service provides three API styles simultaneously.

## Architecture

```
Browser (HTTP/1.1 JSON)
    | Vite proxy (dev) / Envoy (prod)
Go grpcmux server (h2c, port 8080)
    |-- /pb.UserService/*   -> ConnectRPC (JSON over HTTP POST)
    |-- /v1/users/**        -> gRPC-Gateway REST
    +-- /healthz            -> health check
Go gRPC server (port 8081)
    +-- native gRPC (binary, HTTP/2)
```

## Usage

```go
gs := grpcmux.NewServer(...)

// ConnectRPC: Automatically registers all unary methods to /pb.UserService/*
grpcmux.RegisterConnectService(gs, &pb.UserService_ServiceDesc, userSrv)

// gRPC-Gateway REST (optional): /v1/users/*
_ = pb.RegisterUserServiceHandlerServer(ctx, gs.ServeMux(), userSrv)

gs.Start()
```

`RegisterConnectService` uses reflection to automatically iterate over all methods in `ServiceDesc`, generating an HTTP POST handler for each method.
No need to manually write adapter code, no need for `protoc-gen-connect-go`, no dependency on `connectrpc.com/connect`.

## Running

```bash
go build -o bin/connectrpc ./docs/tutorial/restful/cmd/connectrpc
./bin/connectrpc
```

## Testing

### ConnectRPC

```bash
# CreateUser
curl -X POST http://127.0.0.1:8080/pb.UserService/CreateUser \
  -H "Content-Type: application/json" \
  -d '{"name":"Alice","email":"alice@example.com"}'

# GetUser
curl -X POST http://127.0.0.1:8080/pb.UserService/GetUser \
  -H "Content-Type: application/json" \
  -d '{"userId":"<id>"}'

# ListUsers
curl -X POST http://127.0.0.1:8080/pb.UserService/ListUsers \
  -H "Content-Type: application/json" \
  -d '{"page":1,"limit":10}'
```

### REST (gRPC-Gateway)

```bash
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Alice","email":"alice@example.com"}'

curl http://127.0.0.1:8080/v1/users
```

### Frontend

```bash
cd docs/tutorial/restful/frontend/connectrpc
npm install && npm run dev
```

## TLS Configuration

Default is h2c (cleartext HTTP/2), suitable for deployment behind a reverse proxy like Envoy Gateway.

If the Go service needs to handle TLS directly, configure in `config.yaml`:

```yaml
apis:
  tlsCertFile: /path/to/cert.pem
  tlsKeyFile: /path/to/key.pem
```
