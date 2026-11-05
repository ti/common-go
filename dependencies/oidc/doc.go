// Package oidc is a small, dependency-light OpenID Connect relying-party
// toolkit: configuration from one URI, discovery + JWKS, RS256 JWT
// validation, claim-based access rules, and the OAuth client calls a
// backend-for-frontend needs (authorization URL, code / refresh exchange,
// logout URL). The core is framework-neutral; [Provider.Middleware] adapts it
// to net/http and [Provider.AuthFunc] to grpcmux (gRPC, REST and Connect).
//
// # Configuration URI
//
//	oidc://<client_id>:<client_secret>@<issuer host>[/<issuer path>]?scope=...&oidc_logout=false&<claim>=<v1 v2>
//
// The issuer is https://<host><path>; discovery is read from
// <issuer>/.well-known/openid-configuration at startup. The secret may be
// empty (a public client using PKCE); keep the ":". scope defaults to
// "openid profile email". oidc_logout=false selects the Cognito-style logout
// (logout_uri) instead of OIDC RP-initiated logout (post_logout_redirect_uri).
//
// Every other query parameter is a claim access rule, see [AccessPolicy]:
// group=labs%20admin admits tokens whose group (or groups) claim contains
// labs or admin. No rule admits every valid token.
//
// # Use
//
//	p, err := oidc.Open(ctx, os.Getenv("AUTH_URI"))
//	...
//	mux.Handle("/api/", p.Middleware(api))          // net/http
//	claims, ok := oidc.FromContext(r.Context())      // in a handler
//
// or, inside any other framework:
//
//	claims, err := p.Authenticate(ctx, r.Header.Get("Authorization"))
//	switch {
//	case errors.Is(err, oidc.ErrAccessDenied): // 403
//	case err != nil:                           // 401
//	}
//
// # As a dependency
//
// *Provider implements Init(ctx, *url.URL), so it is a regular common-go
// dependency built from its URI by config.Init / dependencies.Init:
//
//	type Dependencies struct {
//		dependencies.Dependency
//		Auth *oidc.Provider `required:"false"` // dependencies.auth: oidc://...
//	}
//
//	gs := grpcmux.NewServer(grpcmux.WithAuthFunc(deps.Auth.AuthFunc()), ...)
//
// A nil *Provider (the URI left empty) makes AuthFunc admit every request,
// which is how a service runs without authentication.
//
// Validation rules (RS256 only, kid required, issuer and aud=client_id,
// exp / nbf / iat with clock skew, typ and token_use markers) and the access
// rules are a cross-language contract: testdata/oidc-jwt-validation-cases.json
// and testdata/oidc-access-rule-cases.json are the same cases other
// implementations (for example the scaffold project's Python and Rust
// backends) are tested against, and error texts are part of that contract.
package oidc
