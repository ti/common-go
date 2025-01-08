# 调用stream模式

使用方法

1. 运行服务

```bash
go run main.go
```

2. 运行grpc到http代理

```bash
cd stream
envoy -c envoy.yaml
```

3. 执行curl stream测试。

> curl中的`-N`参数表示是以字节流的方式读取后端数据。

```bash
curl -i -N -X POST \
   -H "Content-Type:application/json" \
   -d \
'{"name":"test"}' \
 'http://127.0.0.1:9080/v1/stream'
```

### 本地 Stream 模式请求

```bash
curl -i -N -X POST \
   -H "Content-Type:application/json" \
   -H "X-Request-Id:uuid" \
   -d \
'{"name":"test"}' \
 'http://127.0.0.1:8080/v1/stream'
```