package oidc

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Query parameters of the auth URI that configure the provider itself. Every
// other query parameter is an access rule on a JWT claim (see AccessPolicy).
const (
	queryScope      = "scope"
	queryOIDCLogout = "oidc_logout"
)

var (
	// ErrAccessDenied is returned by Provider.Authorize when a validated token
	// does not satisfy the access rules of the auth URI. Callers map it to 403
	// (PermissionDenied), never to 401: the token itself is valid.
	ErrAccessDenied = errors.New("access denied")

	errEmptyAccessRule = fmt.Errorf("%w: access rule has no values", ErrInvalidURI)
)

// ClaimRule requires the claim at Path to contain at least one of Values.
type ClaimRule struct {
	// Path is the claim name as written in the auth URI, e.g. "groups" or
	// "data.email". A dotted path first matches a top-level claim with that
	// exact name, then walks nested objects ({"data":{"email":...}}). No
	// claim name is special: the path is read exactly as written.
	Path   string
	Values []string
}

// AccessPolicy is the set of claim rules parsed from the auth URI query.
//
//	&groups=labs%20admin                       groups contains labs OR admin
//	&data.email=i%40example.com                {"data":{"email":...}} is i@example.com
//	&groups=labs%20admin&sub=uid1%20uid2       groups contains labs or admin, AND sub is uid1 or uid2
//
// Values within one parameter are alternatives (OR), separated by spaces
// (%20 or +) or commas; repeating a parameter adds alternatives. Different
// parameters must all match (AND). An empty policy allows every valid token.
type AccessPolicy struct {
	Rules []ClaimRule
}

// ParseAccessPolicy builds the access policy from the auth URI query. Provider
// options (scope, oidc_logout) are skipped. A rule with no values is an error,
// so that a typo like "groups=" fails at startup instead of silently opening or
// closing access.
func ParseAccessPolicy(query url.Values) (AccessPolicy, error) {
	var policy AccessPolicy

	for _, key := range slices.Sorted(maps.Keys(query)) {
		if key == queryScope || key == queryOIDCLogout {
			continue
		}

		var values []string

		for _, raw := range query[key] {
			for _, value := range splitValues(raw) {
				if !slices.Contains(values, value) {
					values = append(values, value)
				}
			}
		}

		if strings.TrimSpace(key) == "" || len(values) == 0 {
			return AccessPolicy{}, fmt.Errorf("%w: %q", errEmptyAccessRule, key)
		}

		policy.Rules = append(policy.Rules, ClaimRule{Path: key, Values: values})
	}

	return policy, nil
}

// Allow reports nil when the raw claims satisfy every rule, or an error
// wrapping ErrAccessDenied that names the first unmet rule.
func (p AccessPolicy) Allow(raw map[string]any) error {
	for _, rule := range p.Rules {
		actual := claimValues(lookupClaim(raw, rule.Path))
		if !slices.ContainsFunc(rule.Values, func(v string) bool { return slices.Contains(actual, v) }) {
			return fmt.Errorf("%w: claim %q must contain one of [%s]",
				ErrAccessDenied, rule.Path, strings.Join(rule.Values, " "))
		}
	}

	return nil
}

func splitValues(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
}

// lookupClaim resolves path against the claims: an exact top-level key wins,
// otherwise dots descend into nested JSON objects.
func lookupClaim(raw map[string]any, path string) any {
	if value, ok := raw[path]; ok {
		return value
	}

	var current any = raw

	for part := range strings.SplitSeq(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}

		current, ok = object[part]
		if !ok {
			return nil
		}
	}

	return current
}

// claimValues flattens a claim into comparable strings. Strings are split like
// the URI values (so "labs admin" and ["labs","admin"] are equivalent); arrays
// are flattened one level; numbers and booleans use their JSON text.
func claimValues(value any) []string {
	switch v := value.(type) {
	case string:
		return splitValues(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, claimValues(item)...)
		}

		return out
	case json.Number:
		return []string{v.String()}
	case float64:
		return []string{strconv.FormatFloat(v, 'f', -1, 64)}
	case bool:
		return []string{strconv.FormatBool(v)}
	default:
		return nil
	}
}
