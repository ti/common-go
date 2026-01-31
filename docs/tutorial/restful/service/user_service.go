package service

import (
	"context"
	"fmt"
	"time"

	"github.com/ti/common-go/dependencies/database"
	pb "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// UserServiceServer implements UserService
type UserServiceServer struct {
	pb.UnimplementedUserServiceServer
	dep *Dependencies
	cfg *Config
}

// NewUserServiceServer creates a new UserServiceServer instance
func NewUserServiceServer(dep *Dependencies, cfg *Config) *UserServiceServer {
	return &UserServiceServer{
		dep: dep,
		cfg: cfg,
	}
}

// User represents a user in the database
type User struct {
	UserID    int64     `json:"user_id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Age       int32     `json:"age"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateUser creates a new user
func (s *UserServiceServer) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.UserResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Check if database is available
	if s.dep.DB == nil {
		return nil, status.Error(codes.Internal, "database not available")
	}

	// Create user object
	now := time.Now()
	user := &User{
		UserID:    time.Now().UnixNano(), // Generate ID based on timestamp
		Name:      req.Name,
		Email:     req.Email,
		Age:       req.Age,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Insert into database
	_, err := s.dep.DB.Insert(ctx, "users", user)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create user: %v", err))
	}

	// Return response
	return &pb.UserResponse{
		User: &pb.User{
			UserId:    user.UserID,
			Name:      user.Name,
			Email:     user.Email,
			Age:       user.Age,
			CreatedAt: timestamppb.New(user.CreatedAt),
			UpdatedAt: timestamppb.New(user.UpdatedAt),
		},
	}, nil
}

// GetUser retrieves a user by ID
func (s *UserServiceServer) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.UserResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Check if database is available
	if s.dep.DB == nil {
		return nil, status.Error(codes.Internal, "database not available")
	}

	// Query user
	var user User
	err := s.dep.DB.FindOne(ctx, "users", database.C{
		{Key: "user_id", Value: req.UserId, C: database.Eq},
	}, &user)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("user not found: %v", err))
	}

	// Return response
	return &pb.UserResponse{
		User: &pb.User{
			UserId:    user.UserID,
			Name:      user.Name,
			Email:     user.Email,
			Age:       user.Age,
			CreatedAt: timestamppb.New(user.CreatedAt),
			UpdatedAt: timestamppb.New(user.UpdatedAt),
		},
	}, nil
}

// UpdateUser updates an existing user
func (s *UserServiceServer) UpdateUser(ctx context.Context, req *pb.UpdateUserRequest) (*pb.UserResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Check if database is available
	if s.dep.DB == nil {
		return nil, status.Error(codes.Internal, "database not available")
	}

	// Check if user exists
	var existingUser User
	err := s.dep.DB.FindOne(ctx, "users", database.C{
		{Key: "user_id", Value: req.UserId, C: database.Eq},
	}, &existingUser)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("user not found: %v", err))
	}

	// Update user
	updateData := database.D{
		{Key: "name", Value: req.Name},
		{Key: "email", Value: req.Email},
		{Key: "age", Value: req.Age},
		{Key: "updated_at", Value: time.Now()},
	}

	_, err = s.dep.DB.UpdateOne(ctx, "users", database.C{
		{Key: "user_id", Value: req.UserId, C: database.Eq},
	}, updateData)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update user: %v", err))
	}

	// Get updated user
	var updatedUser User
	err = s.dep.DB.FindOne(ctx, "users", database.C{
		{Key: "user_id", Value: req.UserId, C: database.Eq},
	}, &updatedUser)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get updated user: %v", err))
	}

	// Return response
	return &pb.UserResponse{
		User: &pb.User{
			UserId:    updatedUser.UserID,
			Name:      updatedUser.Name,
			Email:     updatedUser.Email,
			Age:       updatedUser.Age,
			CreatedAt: timestamppb.New(updatedUser.CreatedAt),
			UpdatedAt: timestamppb.New(updatedUser.UpdatedAt),
		},
	}, nil
}

// DeleteUser deletes a user by ID
func (s *UserServiceServer) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*pb.DeleteUserResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Check if database is available
	if s.dep.DB == nil {
		return nil, status.Error(codes.Internal, "database not available")
	}

	// Check if user exists
	var existingUser User
	err := s.dep.DB.FindOne(ctx, "users", database.C{
		{Key: "user_id", Value: req.UserId, C: database.Eq},
	}, &existingUser)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("user not found: %v", err))
	}

	// Delete user
	_, err = s.dep.DB.DeleteOne(ctx, "users", database.C{
		{Key: "user_id", Value: req.UserId, C: database.Eq},
	})
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete user: %v", err))
	}

	// Return success response
	return &pb.DeleteUserResponse{
		Success: true,
		Message: fmt.Sprintf("User %d deleted successfully", req.UserId),
	}, nil
}

// ListUsers lists users with pagination
func (s *UserServiceServer) ListUsers(ctx context.Context, req *pb.ListUsersRequest) (*pb.ListUsersResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Check if database is available
	if s.dep.DB == nil {
		return nil, status.Error(codes.Internal, "database not available")
	}

	// Build query request
	queryReq := &database.PageQueryRequest{
		Page:  int(req.Page),
		Limit: int(req.PageSize),
	}

	// Add sort if specified
	if req.SortBy != "" {
		queryReq.Sort = []string{req.SortBy}
	}

	// Query users using direct Find method for simplicity
	var users []User
	err := s.dep.DB.Find(ctx, "users", nil, queryReq.Sort, int(req.PageSize), &users)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list users: %v", err))
	}

	// Get total count
	total, err := s.dep.DB.Count(ctx, "users", nil)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to count users: %v", err))
	}

	// Convert to protobuf
	pbUsers := make([]*pb.User, 0, len(users))
	for _, user := range users {
		pbUsers = append(pbUsers, &pb.User{
			UserId:    user.UserID,
			Name:      user.Name,
			Email:     user.Email,
			Age:       user.Age,
			CreatedAt: timestamppb.New(user.CreatedAt),
			UpdatedAt: timestamppb.New(user.UpdatedAt),
		})
	}

	// Return response
	return &pb.ListUsersResponse{
		Users:    pbUsers,
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
	}, nil
}
