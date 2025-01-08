package mux

import (
	"context"
	"google.golang.org/grpc/metadata"
	"net/http"
	"strings"
)

type authInfoKey struct{}

// AuthInfoFromContext extracts the AuthInfo from the context if it exists.
//
// This API is experimental.
func AuthInfoFromContext(ctx context.Context) (AuthInfo, bool) {
	info := ctx.Value(authInfoKey{})
	if info == nil {
		return nil, false
	}
	return info.(AuthInfo), true
}

// NewIncomingContext new incoming context from header
func NewIncomingContext(r *http.Request) context.Context {
	md := metadata.MD{}
	for k, v := range r.Header {
		k = strings.ToLower(k)
		if strings.HasPrefix(k, "x-") || isPermanentMetaKey(k) {
			md[k] = v
		}
	}
	return metadata.NewIncomingContext(r.Context(), md)
}

// NewAuthInfoContext creates a context with auth info.
func NewAuthInfoContext(ctx context.Context, info AuthInfo) context.Context {
	return context.WithValue(ctx, authInfoKey{}, info)
}

// AuthInfo defines the common interface for the auth information the users are interested in.
type AuthInfo interface {
	AuthType() string
	GetProjectID() string
	GetClientID() string
	GetUserID() string
	GetDeviceID() string
	GetOrganizationID() string
}

// UnimplementedAuthInfo the unimplemented auth info.
type UnimplementedAuthInfo struct{}

// AuthType empty auth t ype
func (UnimplementedAuthInfo) AuthType() string {
	return "Unimplemented"
}

// GetProjectID empty project od
func (UnimplementedAuthInfo) GetProjectID() string {
	return ""
}

// GetClientID empty client id
func (UnimplementedAuthInfo) GetClientID() string {
	return ""
}

// GetUserID empty user id
func (UnimplementedAuthInfo) GetUserID() string {
	return ""
}

// GetDeviceID empty device id
func (UnimplementedAuthInfo) GetDeviceID() string {
	return ""
}

// GetOrganizationID empty organization id
func (UnimplementedAuthInfo) GetOrganizationID() string {
	return ""
}
