//go:build jsonv2

package jsonutil

// TODO: Replace with encoding/json/v2 imports when available in a future Go release.
import (
	"encoding/json"
	"io"
)

// Marshal returns the JSON encoding of v.
// TODO: Switch to json/v2 semantics once encoding/json/v2 is available.
func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal parses the JSON-encoded data and stores the result in the value pointed to by v.
// TODO: Switch to json/v2 semantics once encoding/json/v2 is available.
func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// NewEncoder returns a new json.Encoder that writes to w.
// TODO: Switch to json/v2 encoder once encoding/json/v2 is available.
func NewEncoder(w io.Writer) *json.Encoder {
	return json.NewEncoder(w)
}

// NewDecoder returns a new json.Decoder that reads from r.
// TODO: Switch to json/v2 decoder once encoding/json/v2 is available.
func NewDecoder(r io.Reader) *json.Decoder {
	return json.NewDecoder(r)
}
