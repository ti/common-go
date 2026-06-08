//go:build !jsonv2

package jsonutil

import (
	"encoding/json"
	"io"
)

// Marshal returns the JSON encoding of v.
func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal parses the JSON-encoded data and stores the result in the value pointed to by v.
func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// NewEncoder returns a new json.Encoder that writes to w.
func NewEncoder(w io.Writer) *json.Encoder {
	return json.NewEncoder(w)
}

// NewDecoder returns a new json.Decoder that reads from r.
func NewDecoder(r io.Reader) *json.Decoder {
	return json.NewDecoder(r)
}
