// Package userservice implements example.user.v1.UserService once, for every
// protocol: the same methods serve native gRPC, REST (grpc-gateway) and
// Connect (connect-go v2). Validation comes from the buf.validate rules in the
// proto and is enforced by grpcmux before a method runs, so the methods
// contain business logic only.
package userservice

import (
	"context"
	"strconv"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/database/query"
	userv1 "github.com/ti/common-go/examples/gen/go/example/user/v1"
	"github.com/ti/common-go/examples/gen/go/example/user/v1/userv1connect"
	"github.com/ti/common-go/log"
	"github.com/ti/common-go/tools/snowflake"
)

const (
	table           = "users"
	defaultPageSize = 20
	errorDomain     = "example.user.v1"
)

// Service implements userv1.UserServiceServer (gRPC). Use ConnectHandler for
// connect-go; the two differ only in the streaming method's signature.
type Service struct {
	userv1.UnimplementedUserServiceServer
	db database.Database
}

var _ userv1.UserServiceServer = (*Service)(nil)

// New returns a Service storing users in db.
func New(db database.Database) *Service {
	return &Service{db: db}
}

// userDoc is the stored form of a user. IDs are snowflake integers so they
// sort by creation time and work as cursors.
type userDoc struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Email      string    `json:"email"`
	Age        *int32    `json:"age,omitempty"`
	CreateTime time.Time `json:"create_time"`
	UpdateTime time.Time `json:"update_time"`
}

func (d *userDoc) toProto() *userv1.User {
	return &userv1.User{
		UserId:     strconv.FormatInt(d.ID, 10),
		Name:       d.Name,
		Email:      d.Email,
		Age:        d.Age,
		CreateTime: timestamppb.New(d.CreateTime),
		UpdateTime: timestamppb.New(d.UpdateTime),
	}
}

// CreateUser implements UserService.CreateUser.
func (s *Service) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	in := req.GetUser()
	taken, err := s.db.Exist(ctx, table, database.C{{Key: "email", Value: in.GetEmail()}})
	if err != nil {
		return nil, err
	}
	if taken {
		// AIP-193 style: a canonical code plus a machine-readable reason.
		return nil, errorWithReason(codes.AlreadyExists, "email "+in.GetEmail()+" is already in use",
			"EMAIL_ALREADY_IN_USE", map[string]string{"email": in.GetEmail()})
	}
	now := time.Now().UTC()
	doc := &userDoc{
		ID:         snowflake.ID(),
		Name:       in.GetName(),
		Email:      in.GetEmail(),
		Age:        in.Age,
		CreateTime: now,
		UpdateTime: now,
	}
	if err := s.db.InsertOne(ctx, table, doc); err != nil {
		return nil, err
	}
	// Fields added to the request-scoped logger appear on its access log line.
	log.Inject(ctx, map[string]any{"user_id": doc.ID})
	return &userv1.CreateUserResponse{User: doc.toProto()}, nil
}

// GetUser implements UserService.GetUser.
func (s *Service) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	doc, err := s.find(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &userv1.GetUserResponse{User: doc.toProto()}, nil
}

// UpdateUser implements UserService.UpdateUser. Paths in update_mask select
// the fields to change; an empty mask updates every mutable field.
func (s *Service) UpdateUser(ctx context.Context, req *userv1.UpdateUserRequest) (*userv1.UpdateUserResponse, error) {
	in := req.GetUser()
	doc, err := s.find(ctx, in.GetUserId())
	if err != nil {
		return nil, err
	}
	paths := req.GetUpdateMask().GetPaths()
	if len(paths) == 0 {
		paths = []string{"name", "email", "age"}
	}
	for _, p := range paths {
		switch p {
		case "name":
			doc.Name = in.GetName()
		case "email":
			doc.Email = in.GetEmail()
		case "age":
			doc.Age = in.Age
		default:
			return nil, status.Errorf(codes.InvalidArgument, "update_mask: unknown or immutable field %q", p)
		}
	}
	doc.UpdateTime = time.Now().UTC()
	if _, err := s.db.ReplaceOne(ctx, table, database.C{{Key: "id", Value: doc.ID}}, doc); err != nil {
		return nil, err
	}
	return &userv1.UpdateUserResponse{User: doc.toProto()}, nil
}

// DeleteUser implements UserService.DeleteUser.
func (s *Service) DeleteUser(ctx context.Context, req *userv1.DeleteUserRequest) (*userv1.DeleteUserResponse, error) {
	id, err := parseID(req.GetUserId())
	if err != nil {
		return nil, err
	}
	n, err := s.db.DeleteOne(ctx, table, database.C{{Key: "id", Value: id}})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, notFound(req.GetUserId())
	}
	return &userv1.DeleteUserResponse{}, nil
}

// ListUsers implements UserService.ListUsers: newest first, cursor-based
// (the page token is the last returned ID), so pages stay stable while users
// are added.
func (s *Service) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) (*userv1.ListUsersResponse, error) {
	size := int(req.GetPageSize())
	if size == 0 {
		size = defaultPageSize
	}
	page, err := query.StreamQuery[userDoc](ctx, s.db, table, &database.StreamQueryRequest{
		PageToken: req.GetPageToken(),
		PageField: "id",
		Limit:     size,
		NoCount:   true,
	})
	if err != nil {
		return nil, err
	}
	resp := &userv1.ListUsersResponse{NextPageToken: page.PageToken}
	for _, d := range page.Data {
		resp.Users = append(resp.Users, d.toProto())
	}
	return resp, nil
}

// ExportUsers implements UserService.ExportUsers for gRPC (and REST through
// the gateway).
func (s *Service) ExportUsers(_ *userv1.ExportUsersRequest, stream grpc.ServerStreamingServer[userv1.ExportUsersResponse]) error {
	return s.export(stream.Context(), stream.Send)
}

// export reads users row by row (constant memory) and sends each one.
func (s *Service) export(ctx context.Context, send func(*userv1.ExportUsersResponse) error) error {
	var doc userDoc
	rows, err := s.db.FindRows(ctx, table, database.C{}, []string{"-id"}, 0, &doc)
	if err != nil {
		if database.IsNotFound(err) {
			return nil
		}
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		v, err := rows.Decode()
		if err != nil {
			return err
		}
		d, ok := v.(*userDoc)
		if !ok {
			return status.Errorf(codes.Internal, "unexpected row type %T", v)
		}
		if err := send(&userv1.ExportUsersResponse{User: d.toProto()}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) find(ctx context.Context, userID string) (*userDoc, error) {
	id, err := parseID(userID)
	if err != nil {
		return nil, err
	}
	var doc userDoc
	if err := s.db.FindOne(ctx, table, database.C{{Key: "id", Value: id}}, &doc); err != nil {
		if database.IsNotFound(err) {
			return nil, notFound(userID)
		}
		return nil, err
	}
	return &doc, nil
}

func parseID(userID string) (int64, error) {
	id, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument, "user_id %q is not a valid ID", userID)
	}
	return id, nil
}

func notFound(userID string) error {
	return errorWithReason(codes.NotFound, "user "+userID+" not found", "USER_NOT_FOUND",
		map[string]string{"user_id": userID})
}

// errorWithReason builds a status with a google.rpc.ErrorInfo detail. Clients
// branch on the reason, not on the message text.
func errorWithReason(code codes.Code, msg, reason string, metadata map[string]string) error {
	st, err := status.New(code, msg).WithDetails(&errdetails.ErrorInfo{
		Reason: reason, Domain: errorDomain, Metadata: metadata,
	})
	if err != nil {
		return status.Error(code, msg)
	}
	return st.Err()
}

// ConnectHandler adapts the service to connect-go v2's generated handler
// interface; only the streaming method's signature differs.
type ConnectHandler struct {
	*Service
}

var _ userv1connect.UserServiceHandler = ConnectHandler{}

// ExportUsers implements the connect-go streaming signature.
func (h ConnectHandler) ExportUsers(ctx context.Context, _ *userv1.ExportUsersRequest,
	stream userv1connect.UserServiceExportUsersServerStream,
) error {
	return h.export(ctx, stream.Send)
}
