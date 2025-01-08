# restful

该教程讲解用gRPC 编写 http restful 函数的功能。


## 使用这种方法，构建应用，您将获得：

1. 使用 proto + implement 模式来创造更加现代的协议和实现解耦的开发模式。
2. 利用 proto + validate 时间基本的入参校验，免去业务逻辑中大量验证过程。
3. 程序运行后，您的API 将可以直接通过 HTTP方式和gRPC 方式两种方式进行访问。
4. 您的API将自动生成swagger文档

## 快速运行

```bash
cd tutorial/restful
go run main.go
```

### API 测试

```bash
curl http://127.0.0.1:8080/v1/hello/test
# 返回： {"msg":"hello test"}
```

## 编写步骤

1. 先参考 proto 目录用proto 编写你的API
2. 执行 `make` 命令编译生成 go 文件。
3. 参考 main.go 编写你的服务。
4. `go run main.go` 运行您的服务。

## 附

### Stream 模式请求

```bash
curl -i -N -X POST \
   -H "Content-Type:application/json" \
   -H "X-Request-Id:uuid" \
   -d \
'{"name":"test"}' \
 'http://127.0.0.1:8080/v1/stream'
```