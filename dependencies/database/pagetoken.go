package database

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Page tokens (AIP-158).
//
// A token is the position of the last record of a page: the values of the
// order-by fields. It is sealed with AES-256-GCM, with a digest of the query
// (table, scope, filter, order) as additional data, so that
//
//   - clients cannot read or construct it (opaque),
//   - a token that was altered, truncated or issued for another query fails
//     to open and is rejected with InvalidArgument (bound to the query),
//   - the page size is not part of the binding and may change between pages.
//
// The key is derived from Schema.TokenKey. Without a TokenKey a constant of
// this package is used: the token is then still opaque and still detects
// corruption and reuse on another query, but anybody who reads this source
// can forge one. That is acceptable for the usual case, because a token only
// moves the position inside a query whose scope and filter the server applies
// again on every page; configure TokenKey when positions themselves are
// sensitive. All replicas must use the same key.

const pageTokenVersion = 1

// defaultTokenKey seals page tokens when Schema.TokenKey is empty.
const defaultTokenKey = "github.com/ti/common-go/dependencies/database page token v1"

var errInvalidPageToken = status.Error(codes.InvalidArgument, "invalid page token")

// IsInvalidPageToken reports whether err is the error List returns for a page
// token that is malformed, was altered, or belongs to another query.
func IsInvalidPageToken(err error) bool {
	return err != nil && status.Code(err) == codes.InvalidArgument &&
		status.Convert(err).Message() == status.Convert(errInvalidPageToken).Message()
}

func newTokenSealer(key []byte) (cipher.AEAD, error) {
	if len(key) == 0 {
		key = []byte(defaultTokenKey)
	}
	sum := sha256.Sum256(key)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// queryDigest identifies a list query: everything except the page size and
// the token itself.
func queryDigest(table string, scope any, filter C, orderBy []string) []byte {
	h := sha256.New()
	write := func(s string) {
		_, _ = h.Write([]byte(strconv.Itoa(len(s))))
		_, _ = h.Write([]byte{':'})
		_, _ = h.Write([]byte(s))
	}
	write(table)
	write(digestValue(scope))
	for _, ce := range filter {
		write(ce.Key)
		write(strconv.Itoa(int(ce.C)))
		write(digestValue(ce.Value))
	}
	write("order")
	for _, o := range orderBy {
		write(o)
	}
	return h.Sum(nil)
}

func digestValue(v any) string {
	if t, ok := v.(time.Time); ok {
		return "time:" + formatInstant(t)
	}
	return fmt.Sprintf("%T:%v", v, v)
}

// encodePageToken seals the cursor values for the query identified by digest.
func encodePageToken(aead cipher.AEAD, digest []byte, cursor []any) (string, error) {
	parts := make([]string, len(cursor))
	for i, v := range cursor {
		s, err := encodeCursorValue(v)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	plain, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	_, _ = rand.Read(nonce) // never fails (crypto/rand, Go 1.24+)
	out := make([]byte, 0, 1+len(nonce)+len(plain)+aead.Overhead())
	out = append(out, pageTokenVersion)
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, plain, digest)
	return base64.RawURLEncoding.EncodeToString(out), nil
}

// decodePageToken opens a token for the query identified by digest. An empty
// token is the first page (nil cursor).
func decodePageToken(aead cipher.AEAD, digest []byte, token string, fields int) ([]any, error) {
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < 1+aead.NonceSize()+aead.Overhead() || raw[0] != pageTokenVersion {
		return nil, errInvalidPageToken
	}
	nonce, sealed := raw[1:1+aead.NonceSize()], raw[1+aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, sealed, digest)
	if err != nil {
		return nil, errInvalidPageToken
	}
	var parts []string
	if err := json.Unmarshal(plain, &parts); err != nil || len(parts) != fields {
		return nil, errInvalidPageToken
	}
	cursor := make([]any, len(parts))
	for i, p := range parts {
		v, err := decodeCursorValue(p)
		if err != nil {
			return nil, errInvalidPageToken
		}
		cursor[i] = v
	}
	return cursor, nil
}

// formatInstant writes a time as "<unix seconds>.<nanoseconds>". Unlike
// UnixNano it is exact for every time.Time, the zero time included (UnixNano
// overflows before 1678 and after 2262).
func formatInstant(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10) + "." + strconv.Itoa(t.Nanosecond())
}

func parseInstant(s string) (time.Time, error) {
	secs, nanos, ok := strings.Cut(s, ".")
	if !ok {
		return time.Time{}, errInvalidPageToken
	}
	sec, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	nsec, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil || nsec < 0 || nsec >= int64(time.Second) {
		return time.Time{}, errInvalidPageToken
	}
	return time.Unix(sec, nsec).UTC(), nil
}

// Cursor values are strings with a one-letter kind prefix. Times travel as
// seconds and nanoseconds, so no precision is lost whatever the engine
// stores.
func encodeCursorValue(v any) (string, error) {
	if t, ok := v.(time.Time); ok {
		return "t" + formatInstant(t), nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return "s" + rv.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "i" + strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "u" + strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return "f" + strconv.FormatFloat(rv.Float(), 'g', -1, 64), nil
	case reflect.Bool:
		return "b" + strconv.FormatBool(rv.Bool()), nil
	default:
		return "", status.Errorf(codes.InvalidArgument,
			"cannot order a list by a field of type %T: use a string, number, bool or time.Time field", v)
	}
}

func decodeCursorValue(s string) (any, error) {
	if s == "" {
		return nil, errInvalidPageToken
	}
	kind, body := s[0], s[1:]
	switch kind {
	case 's':
		return body, nil
	case 'i':
		return strconv.ParseInt(body, 10, 64)
	case 'u':
		return strconv.ParseUint(body, 10, 64)
	case 'f':
		return strconv.ParseFloat(body, 64)
	case 'b':
		return strconv.ParseBool(body)
	case 't':
		return parseInstant(body)
	default:
		return nil, errInvalidPageToken
	}
}
