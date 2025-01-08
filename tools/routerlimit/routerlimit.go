// Package routerlimit provide router limit tools
package routerlimit

import (
	"fmt"
	"strings"
	"time"
)

// RouterLimit the rate limit by router
type RouterLimit struct {
	// Default limit: match the header key under the route as a limit.
	Limit []Limit
	// Set whitelist: When the header key value matches a rule, access is allowed.
	Allow []Allow
	// Set a blacklist: when the header key value matches a certain rule, access is prohibited.
	Block []KV
	// default limit.
	Default Default
	// disable the limit
	Disabled bool
}

const (
	// NoLimit no limit
	NoLimit int = -1
	// Block the request
	Block int = 0
)

// Match frequency limit rule
func (r *RouterLimit) Match(path string, header Getter) *LimitValue {
	if r.Disabled {
		return &LimitValue{
			Quota: NoLimit,
		}
	}
	for _, kv := range r.Block {
		headerValue := header.Get(kv.Key)
		if headerValue == kv.Value {
			return &LimitValue{
				Quota:   Block,
				Message: fmt.Sprintf("%s [ %s ] is in the blacklist", getKeyName(kv.Key), headerValue),
			}
		}
	}
	for _, v := range r.Allow {
		if !strings.HasPrefix(path, v.Prefix) {
			continue
		}
		if v.Quota < 0 {
			return &LimitValue{
				Quota: NoLimit,
			}
		}
		limitValue := &LimitValue{
			Quota:    v.Quota,
			Duration: v.Duration,
			Key:      "",
		}
		var targetHeaderKey string
		if len(v.Match) == 0 && v.Duration > 0 {
			limitValue.Key = path
			limitValue.Message = fmt.Sprintf("trace key %s, limit key %s", limitValue.Key, path)
			return limitValue
		}
		// match http header
		for _, headerKV := range v.Match {
			headerValue := header.Get(headerKV.Key)
			if headerKV.Value == headerValue {
				limitValue.Key = headerValue
				limitValue.Message = fmt.Sprintf("trace key %s, limit key %s", limitValue.Key, targetHeaderKey)
				return limitValue
			}
		}
		continue
	}

	for _, v := range r.Limit {
		if !strings.HasPrefix(path, v.Prefix) {
			continue
		}
		return getQuota(v, path, header)
	}
	return getQuota(Limit{
		Headers:  r.Default.Headers,
		Quota:    r.Default.Quota,
		Duration: r.Default.Duration,
	}, path, header)
}

func getQuota(quotaLimit Limit, path string, header Getter) *LimitValue {
	if quotaLimit.Quota <= 0 {
		return &LimitValue{
			Quota: NoLimit,
		}
	}
	limitValue := &LimitValue{
		Quota:    quotaLimit.Quota,
		Duration: quotaLimit.Duration,
		Key:      path,
	}
	var targetHeaderKey string
	for _, headerKey := range quotaLimit.Headers {
		headerValue := header.Get(headerKey)
		if headerValue != "" {
			limitValue.Key += ":" + headerValue
			targetHeaderKey += headerKey
		}
	}
	if limitValue.Key == "" {
		return &LimitValue{
			Quota: NoLimit,
		}
	}
	limitValue.Message = fmt.Sprintf("trace key %s, limit key %s", limitValue.Key, targetHeaderKey)
	return limitValue
}

// Limit data
type Limit struct {
	Prefix   string
	Headers  []string
	Quota    int
	Duration time.Duration
}

// Default the default limit
type Default struct {
	Headers  []string
	Quota    int
	Duration time.Duration
}

// LimitValue the limit value
type LimitValue struct {
	// frequency limit key
	Key string
	// Frequency limit prompt message
	Message string
	// Duration cycle
	Duration time.Duration
	// Quota
	Quota int
}

func getKeyName(key string) string {
	if strings.HasPrefix(key, "x-") {
		return key[2:]
	}
	return key
}

// Allow router
type Allow struct {
	Prefix string
	// when the http header matches x
	Match []KV
	// Limit, when limit is -1, means no limit
	Quota    int
	Duration time.Duration
}

// KV kv
type KV struct {
	Key   string
	Value string
}

// MatchMap matches the Map type
func (r *RouterLimit) MatchMap(path string, data map[string]string) *LimitValue {
	return r.Match(path, mapGetter(data))
}

// MatchHeader matches the http header type
func (r *RouterLimit) MatchHeader(path string, data map[string][]string) *LimitValue {
	return r.Match(path, headerGetter(data))
}

// Getter the getter interface for map or http header
type Getter interface {
	Get(key string) string
}

// mapGetter map implements for map
type mapGetter map[string]string

// Get implement map value
func (m mapGetter) Get(key string) string {
	return m[key]
}

// headerGetter map implements for header
type headerGetter map[string][]string

// Get implement header value
func (m headerGetter) Get(key string) string {
	if value := m[key]; len(value) > 0 {
		return value[0]
	}
	return ""
}
