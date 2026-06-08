# Stream Mode

## Usage

1. Run the service

```bash
go run main.go
```

2. Run the gRPC to HTTP proxy

```bash
cd stream
envoy -c envoy.yaml
```

3. Execute the curl stream test.

> The `-N` parameter in curl means reading backend data as a byte stream.

```bash
curl -i -N -X POST \
   -H "Content-Type:application/json" \
   -d \
'{"name":"test"}' \
 'http://127.0.0.1:9080/v1/stream'
```

### Local Stream Mode Request

```bash
curl -i -N -X POST \
   -H "Content-Type:application/json" \
   -H "X-Request-Id:uuid" \
   -d \
'{"name":"test"}' \
 'http://127.0.0.1:8080/v1/stream'
```
