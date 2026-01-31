package main

import (
	"context"

	"github.com/ti/common-go/grpcmux"
	pb "yourproject/pkg/go/proto"
)

// Example 1: 使用默认下划线格式 (snake_case)
func ExampleDefaultSnakeCase() {
	server := grpcmux.NewServer(
		grpcmux.WithHTTPAddr(":8080"),
		grpcmux.WithGrpcAddr(":8081"),
		// 不设置 UseCamelCase，默认使用下划线格式
	)

	// 注册服务
	pb.RegisterYourServiceServer(server, yourService)
	pb.RegisterYourServiceHandlerServer(context.Background(), server.ServeMux(), yourService)

	// API 响应示例:
	// {
	//   "user_id": 123,
	//   "user_name": "Alice",
	//   "email_address": "alice@example.com",
	//   "created_at": "2024-01-01T00:00:00Z"
	// }
	//
	// 错误响应示例:
	// {
	//   "error": "invalid_argument",
	//   "error_code": 3,
	//   "error_description": "Invalid user input"
	// }

	server.Start()
}

// Example 2: 使用驼峰格式 (camelCase)
func ExampleCamelCase() {
	server := grpcmux.NewServer(
		grpcmux.WithHTTPAddr(":8080"),
		grpcmux.WithGrpcAddr(":8081"),
		grpcmux.WithUseCamelCase(), // 启用驼峰格式
	)

	// 注册服务
	pb.RegisterYourServiceServer(server, yourService)
	pb.RegisterYourServiceHandlerServer(context.Background(), server.ServeMux(), yourService)

	// API 响应示例:
	// {
	//   "userId": 123,
	//   "userName": "Alice",
	//   "emailAddress": "alice@example.com",
	//   "createdAt": "2024-01-01T00:00:00Z"
	// }
	//
	// 错误响应示例:
	// {
	//   "error": "invalid_argument",
	//   "errorCode": 3,
	//   "errorDescription": "Invalid user input"
	// }

	server.Start()
}

// Example 3: 通过配置文件设置格式
type Config struct {
	Apis grpcmux.Config
}

func ExampleWithConfigFile() {
	var cfg Config
	// config.yaml:
	// apis:
	//   grpcAddr: :8081
	//   httpAddr: :8080
	//   metricsAddr: :9090
	//   useCamelCase: true  # 启用驼峰格式

	// 加载配置（假设已经通过 config.Init 加载）
	// cfg.Apis.UseCamelCase = true

	server := grpcmux.NewServer(
		grpcmux.WithConfig(&cfg.Apis),
	)

	// 注册服务
	pb.RegisterYourServiceServer(server, yourService)
	pb.RegisterYourServiceHandlerServer(context.Background(), server.ServeMux(), yourService)

	server.Start()
}

// Example 4: 混合使用选项
func ExampleMixedOptions() {
	server := grpcmux.NewServer(
		grpcmux.WithHTTPAddr(":8080"),
		grpcmux.WithGrpcAddr(":8081"),
		grpcmux.WithUseCamelCase(), // 启用驼峰格式
		grpcmux.WithLoggingOptions( /* 日志选项 */ ),
		grpcmux.WithAuthFunc( /* 认证函数 */ ),
	)

	// 注册服务
	pb.RegisterYourServiceServer(server, yourService)
	pb.RegisterYourServiceHandlerServer(context.Background(), server.ServeMux(), yourService)

	server.Start()
}

// Proto 定义示例
// message User {
//   int64 user_id = 1;
//   string user_name = 2;
//   string email_address = 3;
//   google.protobuf.Timestamp created_at = 4;
//   UserStatus status = 5;
// }
//
// enum UserStatus {
//   USER_STATUS_UNSPECIFIED = 0;
//   USER_STATUS_ACTIVE = 1;
//   USER_STATUS_INACTIVE = 2;
// }

// 格式对比表:
//
// | Proto 字段名      | 下划线格式 (默认)  | 驼峰格式        |
// |------------------|-------------------|----------------|
// | user_id          | user_id           | userId         |
// | user_name        | user_name         | userName       |
// | email_address    | email_address     | emailAddress   |
// | created_at       | created_at        | createdAt      |
// | is_active        | is_active         | isActive       |
// | error_code       | error_code        | errorCode      |
// | error_description| error_description | errorDescription|

// 测试命令:
//
// # 测试下划线格式
// curl -X POST http://localhost:8080/v1/users \
//   -H "Content-Type: application/json" \
//   -d '{"email_address": "alice@example.com", "user_name": "Alice"}'
//
// # 测试驼峰格式
// curl -X POST http://localhost:8080/v1/users \
//   -H "Content-Type: application/json" \
//   -d '{"emailAddress": "alice@example.com", "userName": "Alice"}'
//
// 注意: 请求体的格式应与服务器配置的格式一致
