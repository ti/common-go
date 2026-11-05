package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

type claimsKey struct{}

// NewContext returns ctx carrying validated claims.
func NewContext(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, claims)
}

// FromContext returns the claims stored by [NewContext] or [Provider.Middleware].
func FromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsKey{}).(*Claims)

	return claims, ok && claims != nil
}

// HTTPStatus maps an [Provider.Authenticate] error to its HTTP status:
// 401 for a missing or invalid token, 403 for a failed access rule, 500
// otherwise (and 200 for nil).
func HTTPStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, ErrAccessDenied):
		return http.StatusForbidden
	case errors.Is(err, ErrMissingToken), errors.Is(err, ErrNotBearer), errors.Is(err, ErrInvalidToken):
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

// Middleware authenticates every request with [Provider.Authenticate] and
// stores the claims for [FromContext]. Failures are answered with an RFC
// 6750 WWW-Authenticate header and a JSON body
// {"error","error_description"}.
func (p *Provider) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := p.Authenticate(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			writeAuthError(w, err)

			return
		}

		next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), claims)))
	})
}

func writeAuthError(w http.ResponseWriter, err error) {
	status := HTTPStatus(err)
	code := "internal"

	switch status {
	case http.StatusUnauthorized:
		code = "unauthenticated"

		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	case http.StatusForbidden:
		code = "permission_denied"

		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope"`)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	// The status line is already sent; a failed body write has no recourse.
	writeErr := json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": err.Error()})
	if writeErr != nil {
		return
	}
}
