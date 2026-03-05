# ConnectRPC + gRPC-Gateway + gRPC

在 `camelCase` 的基础上，增加 ConnectRPC 协议支持。同一个服务同时提供三种 API 风格。

## 架构

```
Browser (HTTP/1.1 JSON)
    ↓ Vite proxy (dev) / Envoy (prod)
Go grpcmux server (h2c, port 8080)
    ├── /pb.UserService/*   → ConnectRPC (JSON over HTTP POST)
    ├── /v1/users/**        → gRPC-Gateway REST
    └── /healthz            → health check
Go gRPC server (port 8081)
    └── native gRPC (binary, HTTP/2)
```

## 使用方式

```go
gs := grpcmux.NewServer(...)

// ConnectRPC: 自动注册所有 unary 方法到 /pb.UserService/*
grpcmux.RegisterConnectService(gs, &pb.UserService_ServiceDesc, userSrv)

// gRPC-Gateway REST (可选): /v1/users/*
_ = pb.RegisterUserServiceHandlerServer(ctx, gs.ServeMux(), userSrv)

gs.Start()
```

`RegisterConnectService` 通过反射自动遍历 `ServiceDesc` 中的所有方法，为每个方法生成 HTTP POST handler。
无需手动编写 adapter 代码，无需 `protoc-gen-connect-go`，无需 `connectrpc.com/connect` 依赖。

## 运行

```bash
go build -o bin/connectrpc ./docs/tutorial/restful/cmd/connectrpc
./bin/connectrpc
```

## 测试

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

### 前端

```bash
cd docs/tutorial/restful/frontend/connectrpc
npm install && npm run dev
```

## TLS 配置

默认 h2c（cleartext HTTP/2），适合部署在 Envoy Gateway 等反向代理之后。

如需 Go 服务直接处理 TLS，在 `config.yaml` 中配置：

```yaml
apis:
  tlsCertFile: /path/to/cert.pem
  tlsKeyFile: /path/to/key.pem
```
