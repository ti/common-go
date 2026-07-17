# Go 1.27 Upgrade

This module now requires **Go 1.27** (`go.mod` → `go 1.27`). This doc lists the
Go 1.27 language and standard-library features adopted in this repo, with
before/after references.

## 1. Generic methods — `async.Future.Async`

Go 1.27 allows a method to declare its own type parameters
([go.dev/issue/77273](https://go.dev/issue/77273)), which was not possible
before. `async/async.go` previously had to split `Async` into two pieces
because methods couldn't be generic:

- a package-level generic function `Async[I, O any](f *Future, fn, in) O`
  (type-safe, but callers had to pass `f` as an argument instead of calling
  a method on it)
- a `(*Future).Async(fn any, params ...any) any` fallback that used
  `reflect` to fake generics, with no compile-time type safety

Both are gone. There is now a single generic method:

```go
func (f *Future) Async[I, O any](fn func(ctx context.Context, in I) (O, error), in I) (out O)
```

Callers that used to invoke the free function now call the method directly:

```go
// before
resp := async.Async(future, fn, req)

// after
resp := future.Async(fn, req)
```

Call sites that previously passed multiple positional arguments through the
reflection-based `Async(fn any, params ...any) any` (e.g.
`dependencies/dependencies.go`, `graceful/graceful.go`) now wrap their
arguments in a small struct and pass a single closure, since the generic
method takes exactly one input value `I` and one output value `O`. This
trades a bit of call-site boilerplate for full compile-time type checking —
no `reflect` involved.

Affected files: `async/async.go`, `async/async_test.go`,
`graceful/graceful.go`, `dependencies/dependencies.go`.

## 2. `encoding/json/v2`

Go 1.27 ships `encoding/json/v2` (and `encoding/json/jsontext`) in the
standard library — no `GOEXPERIMENT` flag needed. `encoding/json` (v1) is
now backed by the same implementation, but `v2` is the forward-looking API
and has measurably faster `Unmarshal`.

All internal `Marshal`/`Unmarshal` call sites were switched from
`encoding/json` to `encoding/json/v2`:

- `grpcmux/connect.go`
- `dependencies/http/http.go`, `dependencies/http/example/example.go`
- `dependencies/sql/sql.go`, `dependencies/sql/adapters/postgres/sql.go`
- `dependencies/mongo/query_page.go`, `dependencies/mongo/codecs/marshal.go`
- `dependencies/mqlru/mqlru.go`
- `dependencies/database/mock/error.go` (and its test)

`json.RawMessage` (v1) has no direct re-export under `v2`; its analog is
`encoding/json/jsontext.Value`, which implements the same
`MarshalJSON`/`UnmarshalJSON` pair, so it works as a drop-in with
`json/v2`'s `Marshal`/`Unmarshal`. `dependencies/mongo/codecs/marshal.go`
uses `jsontext.Value` where it previously used `json.RawMessage`.

One file, `grpcmux/logging/audit.go`, still imports `encoding/json` (v1):
its `ParseLoggerConfig(config json.RawMessage) (...)` method signature is
fixed by the external `google.golang.org/grpc/authz/audit.LoggerBuilder`
interface, which is defined against `encoding/json.RawMessage`. This is a
boundary constraint from a third-party API, not something this repo
controls.

## 3. Standard-library `uuid` package

Go 1.27 adds a `uuid` package to the standard library
([RFC 9562](https://www.rfc-editor.org/rfc/rfc9562.html)), so the
`github.com/google/uuid` dependency is no longer needed. `uuid.New()` has
the same signature and behavior as the third-party package for the `New()`
+ `.String()` usage in this repo.

Replaced in:

- `grpcmux/mux/interceptor.go` (request ID generation)
- `dependencies/mqlru/mqlru.go` (instance ID fallback)
- `dependencies/broker/kafka/kafka.go` (message key)

`github.com/google/uuid` was removed from `go.mod` via `go mod tidy`.

## 4. HTTP/2 RFC 9218 client priority (`net/http.Server.DisableClientPriority`)

Go 1.27's HTTP/2 server implementation understands client-declared stream
priority per [RFC 9218](https://www.rfc-editor.org/rfc/rfc9218.html) and
serves higher-priority streams first by default — previously all streams
were served round-robin. `golang.org/x/net/http2` (used for h2c in
`grpcmux/server.go`) also picks this up automatically on Go 1.27+, since it
reads the same `http.Server.DisableClientPriority` field.

`grpcmux` exposes this as an explicit opt-out, `WithDisableClientPriority()`
(`grpcmux/options.go`), for callers who want the old round-robin scheduling
back. It's wired into both the TLS (`ListenAndServeTLS`) and h2c
(`http2.ConfigureServer`) code paths in `grpcmux/server.go`, since both
read `http.Server.DisableClientPriority`.

## 5. `math/rand/v2` generic `Rand.N` method

Go 1.27 makes `(*rand.Rand).N` generic over any integer type — matching the
top-level `rand.N[Int]` function, which was already generic but couldn't be
mirrored on `*Rand` before generic methods existed.

`tools/snowflake/snowflake.go`'s `getHostHashNumber` used to draw a random
node number via `crypto/rand.Int(rand.Reader, big.NewInt(MaxNodeNumber))`.
It now seeds a `math/rand/v2` PCG source from `crypto/rand` (keeping the
result cryptographically unpredictable) and calls the new generic method:

```go
src := randv2.NewPCG(seedHi, seedLo)
return randv2.New(src).N(int64(MaxNodeNumber))
```

## Verification

- `go build ./...`, `go vet ./...`, and `go test ./... -race` all pass
  after every change described above.
- The proto-array marshal/unmarshal path in
  `dependencies/mongo/codecs/marshal.go` (using `jsontext.Value`) was
  exercised manually with a round-trip test in addition to the existing
  test suite, since it has no dedicated unit test.
