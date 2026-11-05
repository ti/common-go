# OIDC authentication (`dependencies/oidc`)

`dependencies/oidc` is an OpenID Connect relying-party toolkit configured from one URI:

- discovery and JWKS, with key rotation handled by refreshing on an unknown `kid`;
- RS256 JWT validation;
- claim-based access rules;
- the OAuth calls a backend-for-frontend needs: authorization URL, code/refresh/client-credentials exchange, and logout URL.

It plugs into grpcmux, where one auth function covers gRPC, REST and Connect, and it also works as plain `net/http` middleware.

## The auth URI

```
oidc://<client_id>:<client_secret>@<issuer host>[/<issuer path>]?scope=...&oidc_logout=false&<claim>=<v1 v2>
```

- **Issuer:** `https://<host><path>`. Discovery (`/.well-known/openid-configuration`) and the JWKS are fetched at startup, and startup fails if either is unreachable, so authentication never fails open.
- **Client secret:** may be empty for a public client using PKCE. Keep the `:`.
- **`scope`:** defaults to `openid profile email`.
- **`oidc_logout=false`:** selects the Cognito-style `logout_uri` logout instead of OIDC RP-initiated logout.
- **Any other parameter is an access rule** on a JWT claim:

| Rule | Admits tokens whose |
|---|---|
| `group=labs%20admin` | `group` or `groups` claim contains `labs` or `admin` |
| `user.role=labs` | `user.role` (exact key `user.role` first, then nested `{"user":{"role":...}}`) contains `labs` |
| `group=admin&sub=uid1%20uid2` | group contains `admin` **and** sub is `uid1` or `uid2` |

Within one parameter, values separated by spaces or commas are alternatives (OR); different parameters must all match (AND). String and array claims are treated the same. A rule with no values (`group=`) fails at startup. A URI with no rules admits every valid token.

## As a dependency with grpcmux

```go
type Dependencies struct {
	dependencies.Dependency
	Auth *oidc.Provider `required:"false"` // dependencies.auth: oidc://client:secret@account.example.com?group=admin
}

gs := grpcmux.NewServer(
	grpcmux.WithConfig(&cfg.Apis),
	grpcmux.WithAuthFunc(cfg.Dependencies.Auth.AuthFunc()),
	grpcmux.WithNoAuthPrefixes("/auth.v1.AuthService/Authorize"), // public endpoints
)
```

`AuthFunc` reads the `authorization` metadata, which grpcmux fills from the HTTP header for REST and Connect requests. It then validates the token and applies the access rules:

- A missing or invalid token returns **UNAUTHENTICATED** (401).
- A valid token that fails a rule returns **PERMISSION_DENIED** (403), and the user's `sub` is added to the request log.
- On success the claims are in the context (`oidc.FromContext(ctx)`), and `user_id`, `client_id` and `project_id` appear on the access log line through `mux.AuthInfo`.

On a nil `*Provider`, which happens when the optional dependency is left unconfigured, `AuthFunc` admits every request. That gives you an "authentication off" mode without a separate code path.

To add your own steps, wrap it, for example to inject a trace ID before the check:

```go
authenticate := provider.AuthFunc()
gs := grpcmux.NewServer(grpcmux.WithAuthFunc(func(ctx context.Context) (context.Context, error) {
	injectTraceContext(ctx)
	return authenticate(ctx)
}))
```

In a handler:

```go
claims, ok := oidc.FromContext(ctx)
role, _ := claims.Lookup[string]("user.role")   // any claim, dotted paths reach nested objects
groups := claims.AllGroups()                      // union of "groups" and "group"
```

## Without grpcmux

```go
p, err := oidc.Open(ctx, os.Getenv("AUTH_URI"))
mux.Handle("/api/", p.Middleware(api))   // net/http; 401/403 with WWW-Authenticate and a JSON body

claims, err := p.Authenticate(ctx, r.Header.Get("Authorization"))
status := oidc.HTTPStatus(err)            // 200, 401, 403 or 500
```

## Backend-for-frontend calls

```go
url := p.AuthorizationURL(oidc.AuthRequest{RedirectURI: cb, State: state, CodeChallenge: challenge})
tok, err := p.Exchange(ctx, oidc.AuthorizationCode{Code: code, RedirectURI: cb, CodeVerifier: verifier})
tok, err  = p.Exchange(ctx, oidc.RefreshToken(refresh))
tok, err  = p.Exchange(ctx, oidc.ClientCredentials{})
logout, err := p.LogoutURL(postLogoutRedirect, tok.IDToken)
```

A non-200 response from the token endpoint is returned as a `*oidc.TokenError`, which carries the issuer's RFC 6749 `error` and `error_description`. Get it with `errors.AsType[*oidc.TokenError](err)`.

## Contract

The validation rules and access-rule semantics are a cross-language contract. The validation rules are: RS256 only, `kid` required, issuer and `aud` = client id, `exp`/`nbf`/`iat` checked with 60s clock skew, and `typ`/`token_use` markers. `dependencies/oidc/testdata/*.json` holds the same cases that other implementations are tested against, for example the scaffold project's Python and Rust backends. Error texts are part of that contract, so change them only together with the cases.
