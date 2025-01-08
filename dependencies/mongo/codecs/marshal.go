package codecs

import (
	"encoding/json"
	"fmt"
	"reflect"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Marshal parse any to json string.
func Marshal(val any) ([]byte, error) {
	rv := reflect.ValueOf(val)
	kind := rv.Kind()
	switch kind {
	case reflect.Array, reflect.Slice:
		if !isProtoSlice(val) {
			return json.Marshal(val)
		}
		return marshalProtoArray(val)
	default:
		return encodeToJSON(val)
	}
}

// Unmarshal parses the json string to data.
func Unmarshal(data []byte, val any) error {
	if data[0] == '[' {
		rv := reflect.ValueOf(val)
		kind := rv.Kind()
		if kind == reflect.Pointer {
			if isProtoSlice(reflect.Indirect(rv).Interface()) {
				return unmarshalProtoArray(data, val)
			}
		}
		return json.Unmarshal(data, val)
	}
	rv := reflect.ValueOf(val)
	if kind := rv.Kind(); kind == reflect.Pointer {
		if pv, ok := val.(proto.Message); ok {
			return protojson.Unmarshal(data, pv)
		}
	}
	return json.Unmarshal(data, &val)
}

func isProtoSlice(arr any) bool {
	arrType := reflect.TypeOf(arr)
	if arrType.Kind() != reflect.Slice {
		return false
	}
	elemType := arrType.Elem()
	if protoMessageType := reflect.TypeOf((*proto.Message)(nil)).Elem(); !elemType.Implements(protoMessageType) {
		return false
	}
	return true
}

// EncodeToJSON encode data to json
func encodeToJSON(data any) ([]byte, error) {
	rv := reflect.ValueOf(data)
	if kind := rv.Kind(); kind == reflect.Pointer {
		if pv, ok := data.(proto.Message); ok {
			return (protojson.MarshalOptions{
				UseProtoNames: true,
			}).Marshal(pv)
		}
	}
	return json.Marshal(data)
}

func marshalProtoArray(data any) ([]byte, error) {
	arrValue := reflect.ValueOf(data)
	raw := make([]json.RawMessage, arrValue.Len())
	for i := 0; i < arrValue.Len(); i++ {
		elemValue := arrValue.Index(i)
		r, err := protojson.Marshal(elemValue.Interface().(proto.Message))
		if err != nil {
			return nil, err
		}
		raw[i] = r
	}
	return json.Marshal(raw)
}

func unmarshalProtoArray(rawBytes []byte, data any) error {
	var raw []json.RawMessage
	err := json.Unmarshal(rawBytes, &raw)
	if err != nil {
		return err
	}
	sliceValue := reflect.ValueOf(data).Elem()
	elemType := sliceValue.Type().Elem().Elem()
	for i, r := range raw {
		newElement := reflect.New(elemType).Interface()
		err = protojson.Unmarshal(r, newElement.(proto.Message))
		if err != nil {
			return fmt.Errorf("failed to unmarshal raw JSON at index %d: %w", i, err)
		}
		sliceValue.Set(reflect.Append(sliceValue, reflect.ValueOf(newElement)))
	}
	return nil
}
