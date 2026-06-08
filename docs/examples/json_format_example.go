//go:build ignore

package main

import (
	"context"

	"github.com/ti/common-go/grpcmux"
	pb "yourproject/pkg/go/proto"
)

// Example 1: Using the default snake_case format
func ExampleDefaultSnakeCase() {
	server := grpcmux.NewServer(
		grpcmux.WithHTTPAddr(":8080"),
		grpcmux.WithGrpcAddr(":8081"),
		// Without setting UseCamelCase, defaults to snake_case format
	)

	// Register service
	pb.RegisterYourServiceServer(server, yourService)
	pb.RegisterYourServiceHandlerServer(context.Background(), server.ServeMux(), yourService)

	// API response example:
	// {
	//   "user_id": 123,
	//   "user_name": "Alice",
	//   "email_address": "alice@example.com",
	//   "created_at": "2024-01-01T00:00:00Z"
	// }
	//
	// Error response example:
	// {
	//   "error": "invalid_argument",
	//   "error_code": 3,
	//   "error_description": "Invalid user input"
	// }

	server.Start()
}

// Example 2: Using camelCase format
func ExampleCamelCase() {
	server := grpcmux.NewServer(
		grpcmux.WithHTTPAddr(":8080"),
		grpcmux.WithGrpcAddr(":8081"),
		grpcmux.WithUseCamelCase(), // Enable camelCase format
	)

	// Register service
	pb.RegisterYourServiceServer(server, yourService)
	pb.RegisterYourServiceHandlerServer(context.Background(), server.ServeMux(), yourService)

	// API response example:
	// {
	//   "userId": 123,
	//   "userName": "Alice",
	//   "emailAddress": "alice@example.com",
	//   "createdAt": "2024-01-01T00:00:00Z"
	// }
	//
	// Error response example:
	// {
	//   "error": "invalid_argument",
	//   "errorCode": 3,
	//   "errorDescription": "Invalid user input"
	// }

	server.Start()
}

// Example 3: Setting format via config file
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
	//   useCamelCase: true  # Enable camelCase format

	// Load config (assuming already loaded via config.Init)
	// cfg.Apis.UseCamelCase = true

	server := grpcmux.NewServer(
		grpcmux.WithConfig(&cfg.Apis),
	)

	// Register service
	pb.RegisterYourServiceServer(server, yourService)
	pb.RegisterYourServiceHandlerServer(context.Background(), server.ServeMux(), yourService)

	server.Start()
}

// Example 4: Using mixed options
func ExampleMixedOptions() {
	server := grpcmux.NewServer(
		grpcmux.WithHTTPAddr(":8080"),
		grpcmux.WithGrpcAddr(":8081"),
		grpcmux.WithUseCamelCase(), // Enable camelCase format
		grpcmux.WithLoggingOptions( /* logging options */ ),
		grpcmux.WithAuthFunc( /* auth function */ ),
	)

	// Register service
	pb.RegisterYourServiceServer(server, yourService)
	pb.RegisterYourServiceHandlerServer(context.Background(), server.ServeMux(), yourService)

	server.Start()
}

// Proto definition example
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

// Format comparison table:
//
// | Proto Field Name  | snake_case (default) | camelCase        |
// |------------------|---------------------|----------------|
// | user_id          | user_id           | userId         |
// | user_name        | user_name         | userName       |
// | email_address    | email_address     | emailAddress   |
// | created_at       | created_at        | createdAt      |
// | is_active        | is_active         | isActive       |
// | error_code       | error_code        | errorCode      |
// | error_description| error_description | errorDescription|

// Test commands:
//
// # Test snake_case format
// curl -X POST http://localhost:8080/v1/users \
//   -H "Content-Type: application/json" \
//   -d '{"email_address": "alice@example.com", "user_name": "Alice"}'
//
// # Test camelCase format
// curl -X POST http://localhost:8080/v1/users \
//   -H "Content-Type: application/json" \
//   -d '{"emailAddress": "alice@example.com", "userName": "Alice"}'
//
// Note: The request body format should match the server configuration
