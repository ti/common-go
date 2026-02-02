package service

import (
	"context"
	"fmt"
	"time"

	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/database/query"
	pb "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
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
// This struct demonstrates proper handling of all protobuf wrapper types
type User struct {
	UserID    int64     `json:"user_id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Optional fields using pointers to distinguish "not set" from "zero value"
	Age                   *int32   `json:"age,omitempty"`
	IsActive              *bool    `json:"is_active,omitempty"`
	IsVerified            *bool    `json:"is_verified,omitempty"`
	IsPremium             *bool    `json:"is_premium,omitempty"`
	PhoneNumber           *string  `json:"phone_number,omitempty"`
	Address               *string  `json:"address,omitempty"`
	Bio                   *string  `json:"bio,omitempty"`
	ReferrerID            *int64   `json:"referrer_id,omitempty"`
	LoginCount            *int64   `json:"login_count,omitempty"`
	AccountBalance        *float64 `json:"account_balance,omitempty"`
	Rating                *float64 `json:"rating,omitempty"`
	DiscountRate          *float32 `json:"discount_rate,omitempty"`
	FailedLoginAttempts   *uint32  `json:"failed_login_attempts,omitempty"`
	TotalSpent            *uint64  `json:"total_spent,omitempty"`
	ProfilePicture        []byte   `json:"profile_picture,omitempty"`
	PublicKey             []byte   `json:"public_key,omitempty"`
	LastLoginAt           *time.Time `json:"last_login_at,omitempty"`
	EmailVerifiedAt       *time.Time `json:"email_verified_at,omitempty"`
	PremiumExpiresAt      *time.Time `json:"premium_expires_at,omitempty"`
}

// userToProto converts User struct to protobuf User message
func userToProto(user *User) *pb.User {
	pbUser := &pb.User{
		UserId:    user.UserID,
		Name:      user.Name,
		Email:     user.Email,
		CreatedAt: timestamppb.New(user.CreatedAt),
		UpdatedAt: timestamppb.New(user.UpdatedAt),
	}

	// Convert optional fields using wrapper types
	if user.Age != nil {
		pbUser.Age = wrapperspb.Int32(*user.Age)
	}
	if user.IsActive != nil {
		pbUser.IsActive = wrapperspb.Bool(*user.IsActive)
	}
	if user.IsVerified != nil {
		pbUser.IsVerified = wrapperspb.Bool(*user.IsVerified)
	}
	if user.IsPremium != nil {
		pbUser.IsPremium = wrapperspb.Bool(*user.IsPremium)
	}
	if user.PhoneNumber != nil {
		pbUser.PhoneNumber = wrapperspb.String(*user.PhoneNumber)
	}
	if user.Address != nil {
		pbUser.Address = wrapperspb.String(*user.Address)
	}
	if user.Bio != nil {
		pbUser.Bio = wrapperspb.String(*user.Bio)
	}
	if user.ReferrerID != nil {
		pbUser.ReferrerId = wrapperspb.Int64(*user.ReferrerID)
	}
	if user.LoginCount != nil {
		pbUser.LoginCount = wrapperspb.Int64(*user.LoginCount)
	}
	if user.AccountBalance != nil {
		pbUser.AccountBalance = wrapperspb.Double(*user.AccountBalance)
	}
	if user.Rating != nil {
		pbUser.Rating = wrapperspb.Double(*user.Rating)
	}
	if user.DiscountRate != nil {
		pbUser.DiscountRate = wrapperspb.Float(*user.DiscountRate)
	}
	if user.FailedLoginAttempts != nil {
		pbUser.FailedLoginAttempts = wrapperspb.UInt32(*user.FailedLoginAttempts)
	}
	if user.TotalSpent != nil {
		pbUser.TotalSpent = wrapperspb.UInt64(*user.TotalSpent)
	}
	if user.ProfilePicture != nil {
		pbUser.ProfilePicture = wrapperspb.Bytes(user.ProfilePicture)
	}
	if user.PublicKey != nil {
		pbUser.PublicKey = wrapperspb.Bytes(user.PublicKey)
	}
	if user.LastLoginAt != nil {
		pbUser.LastLoginAt = timestamppb.New(*user.LastLoginAt)
	}
	if user.EmailVerifiedAt != nil {
		pbUser.EmailVerifiedAt = timestamppb.New(*user.EmailVerifiedAt)
	}
	if user.PremiumExpiresAt != nil {
		pbUser.PremiumExpiresAt = timestamppb.New(*user.PremiumExpiresAt)
	}

	return pbUser
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

	// Create user object with required fields
	now := time.Now()
	user := &User{
		UserID:    time.Now().UnixNano(),
		Name:      req.Name,
		Email:     req.Email,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Set optional fields from request
	if req.Age != nil {
		age := req.Age.Value
		user.Age = &age
	}
	if req.IsPremium != nil {
		isPremium := req.IsPremium.Value
		user.IsPremium = &isPremium
	}
	if req.PhoneNumber != nil {
		phone := req.PhoneNumber.Value
		user.PhoneNumber = &phone
	}
	if req.Address != nil {
		addr := req.Address.Value
		user.Address = &addr
	}
	if req.Bio != nil {
		bio := req.Bio.Value
		user.Bio = &bio
	}
	if req.ReferrerId != nil {
		refID := req.ReferrerId.Value
		user.ReferrerID = &refID
	}
	if req.AccountBalance != nil {
		balance := req.AccountBalance.Value
		user.AccountBalance = &balance
	}
	if req.DiscountRate != nil {
		discount := req.DiscountRate.Value
		user.DiscountRate = &discount
	}
	if req.ProfilePicture != nil {
		user.ProfilePicture = req.ProfilePicture.Value
	}

	// Insert into database
	_, err := s.dep.DB.Insert(ctx, "users", user)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create user: %v", err))
	}

	// Return response
	return &pb.UserResponse{
		User: userToProto(user),
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
		User: userToProto(&user),
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

	// Build update document with only provided fields
	updates := database.D{}

	if req.Name != nil {
		updates = append(updates, database.E{Key: "name", Value: req.Name.Value})
	}
	if req.Email != nil {
		updates = append(updates, database.E{Key: "email", Value: req.Email.Value})
	}
	if req.Age != nil {
		age := req.Age.Value
		updates = append(updates, database.E{Key: "age", Value: &age})
	}
	if req.IsActive != nil {
		isActive := req.IsActive.Value
		updates = append(updates, database.E{Key: "is_active", Value: &isActive})
	}
	if req.IsVerified != nil {
		isVerified := req.IsVerified.Value
		updates = append(updates, database.E{Key: "is_verified", Value: &isVerified})
	}
	if req.IsPremium != nil {
		isPremium := req.IsPremium.Value
		updates = append(updates, database.E{Key: "is_premium", Value: &isPremium})
	}
	if req.PhoneNumber != nil {
		phone := req.PhoneNumber.Value
		updates = append(updates, database.E{Key: "phone_number", Value: &phone})
	}
	if req.Address != nil {
		addr := req.Address.Value
		updates = append(updates, database.E{Key: "address", Value: &addr})
	}
	if req.Bio != nil {
		bio := req.Bio.Value
		updates = append(updates, database.E{Key: "bio", Value: &bio})
	}
	if req.ReferrerId != nil {
		refID := req.ReferrerId.Value
		updates = append(updates, database.E{Key: "referrer_id", Value: &refID})
	}
	if req.AccountBalance != nil {
		balance := req.AccountBalance.Value
		updates = append(updates, database.E{Key: "account_balance", Value: &balance})
	}
	if req.Rating != nil {
		rating := req.Rating.Value
		updates = append(updates, database.E{Key: "rating", Value: &rating})
	}
	if req.DiscountRate != nil {
		discount := req.DiscountRate.Value
		updates = append(updates, database.E{Key: "discount_rate", Value: &discount})
	}
	if req.ProfilePicture != nil {
		updates = append(updates, database.E{Key: "profile_picture", Value: req.ProfilePicture.Value})
	}

	// Always update updated_at
	updates = append(updates, database.E{Key: "updated_at", Value: time.Now()})

	// Update user
	_, err = s.dep.DB.UpdateOne(ctx, "users", database.C{
		{Key: "user_id", Value: req.UserId, C: database.Eq},
	}, updates)
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
		User: userToProto(&updatedUser),
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

// ListUsers lists users with page-based pagination using PageQuery
func (s *UserServiceServer) ListUsers(ctx context.Context, req *pb.PageQueryRequest) (*pb.PageUsersResponse, error) {
	// Check if database is available
	if s.dep.DB == nil {
		return nil, status.Error(codes.Internal, "database not available")
	}

	// Build PageQueryRequest for database layer
	dbReq := &database.PageQueryRequest{
		Page:  int(req.Page),
		Limit: int(req.Limit),
		Sort:  req.Sort,
	}

	// Set default values
	if dbReq.Page <= 0 {
		dbReq.Page = 1
	}
	if dbReq.Limit <= 0 {
		dbReq.Limit = 10
	}

	// Use query.PageQuery for efficient pagination
	resp, err := query.PageQuery[User](ctx, s.dep.DB, "users", dbReq)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to query users: %v", err))
	}

	// Convert to protobuf
	pbUsers := make([]*pb.User, 0, len(resp.Data))
	for _, user := range resp.Data {
		pbUsers = append(pbUsers, userToProto(user))
	}

	// Return response
	return &pb.PageUsersResponse{
		Data:  pbUsers,
		Total: resp.Total,
	}, nil
}

// StreamUsers implements cursor-based pagination using StreamQuery
func (s *UserServiceServer) StreamUsers(ctx context.Context, req *pb.StreamQueryRequest) (*pb.StreamUsersResponse, error) {
	// Check if database is available
	if s.dep.DB == nil {
		return nil, status.Error(codes.Internal, "database not available")
	}

	// Build StreamQueryRequest for database layer
	dbReq := &database.StreamQueryRequest{
		PageToken: req.PageToken,
		PageField: "user_id",  // Use user_id as the cursor field
		Limit:     int(req.Limit),
		Ascending: req.Ascending,
	}

	// Set default limit
	if dbReq.Limit <= 0 {
		dbReq.Limit = 10
	}

	// Use query.StreamQuery for cursor-based pagination
	resp, err := query.StreamQuery[User](ctx, s.dep.DB, "users", dbReq)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to stream users: %v", err))
	}

	// Convert to protobuf
	pbUsers := make([]*pb.User, 0, len(resp.Data))
	for _, user := range resp.Data {
		pbUsers = append(pbUsers, userToProto(user))
	}

	// Return response
	return &pb.StreamUsersResponse{
		PageToken: resp.PageToken,
		Data:      pbUsers,
		Total:     resp.Total,
	}, nil
}
