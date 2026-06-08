// Package jsonutil provides a thin abstraction over Go's encoding/json package.
//
// It is designed to facilitate a future migration to encoding/json/v2 (expected
// in Go 1.25+) by centralizing JSON operations behind a single import path.
//
// Migration strategy:
//   - Default build (no tags): uses encoding/json (v1).
//   - Build with -tags=jsonv2: uses encoding/json/v2 when available.
//
// Internal packages should import this package instead of encoding/json directly
// when they want to opt into the future v2 behavior transparently.
package jsonutil
