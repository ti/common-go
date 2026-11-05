package oidc

import (
	"context"
	"errors"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/metadata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/grpcmux/mux"
	"github.com/ti/common-go/log"
)

// AuthFunc returns a grpcmux auth function (grpcmux.WithAuthFunc) that
// authenticates every request with [Provider.Authenticate].
//
// grpcmux runs it for native gRPC calls and for HTTP requests (REST, Connect)
// alike: its HTTP middleware copies the Authorization header into incoming
// metadata first. On success the context carries the claims ([FromContext])
// and a mux.AuthInfo, so grpcmux stamps user_id (sub) and client_id onto the
// request's access log line. Errors are gRPC statuses:
//
//   - missing, non-Bearer or invalid token → UNAUTHENTICATED (HTTP 401);
//   - valid token that fails an access rule → PERMISSION_DENIED (HTTP 403),
//     with the denied user's sub added to the request log.
//
// Called on a nil *Provider, the function admits every request unchanged, so
// an optional `Auth *oidc.Provider` dependency left unconfigured means
// "authentication off" without a separate code path.
func (p *Provider) AuthFunc() func(ctx context.Context) (context.Context, error) {
	return func(ctx context.Context) (context.Context, error) {
		if p == nil {
			return ctx, nil
		}

		claims, err := p.Authenticate(ctx, metadata.ExtractIncoming(ctx).Get("authorization"))

		switch {
		case errors.Is(err, ErrAccessDenied):
			log.Inject(ctx, map[string]any{"user_id": claims.Sub})

			return ctx, status.Error(codes.PermissionDenied, err.Error())
		case errors.Is(err, ErrInvalidToken):
			return ctx, status.Error(codes.Unauthenticated, "invalid token: "+err.Error())
		case err != nil:
			return ctx, status.Error(codes.Unauthenticated, err.Error())
		}

		ctx = NewContext(ctx, claims)

		return mux.NewAuthInfoContext(ctx, p.AuthInfo(claims)), nil
	}
}

// AuthInfo adapts validated claims to mux.AuthInfo: user_id is the subject,
// client_id this provider's client (audience validation guarantees the token
// was issued for it, also when aud is an array), project_id the project_id
// claim when the issuer sets one.
func (p *Provider) AuthInfo(claims *Claims) mux.AuthInfo {
	return authInfo{userID: claims.Sub, clientID: p.ClientID(), projectID: claims.ProjectID}
}

type authInfo struct {
	mux.UnimplementedAuthInfo

	userID    string
	clientID  string
	projectID string
}

func (a authInfo) AuthType() string     { return "oidc" }
func (a authInfo) GetUserID() string    { return a.userID }
func (a authInfo) GetClientID() string  { return a.clientID }
func (a authInfo) GetProjectID() string { return a.projectID }
