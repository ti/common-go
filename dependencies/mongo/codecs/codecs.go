package codecs

import (
	"bytes"
	"errors"
	"reflect"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"google.golang.org/protobuf/types/known/timestamppb"

	"go.mongodb.org/mongo-driver/bson/bsoncodec"
	"go.mongodb.org/mongo-driver/bson/bsonrw"
)

var (
	// Protobuf’s wrappers types
	boolValueType   = reflect.TypeOf(wrapperspb.BoolValue{})
	bytesValueType  = reflect.TypeOf(wrapperspb.BytesValue{})
	doubleValueType = reflect.TypeOf(wrapperspb.DoubleValue{})
	floatValueType  = reflect.TypeOf(wrapperspb.FloatValue{})
	int32ValueType  = reflect.TypeOf(wrapperspb.Int32Value{})
	int64ValueType  = reflect.TypeOf(wrapperspb.Int64Value{})
	stringValueType = reflect.TypeOf(wrapperspb.StringValue{})
	uint32ValueType = reflect.TypeOf(wrapperspb.UInt32Value{})
	uint64ValueType = reflect.TypeOf(wrapperspb.UInt64Value{})

	// Protobuf Timestamp type
	timestampType = reflect.TypeOf(timestamppb.Timestamp{})

	// Time type
	timeType = reflect.TypeOf(time.Time{})

	// Codecs
	wrapperValueCodecRef = &wrapperValueCodec{}
	timestampCodecRef    = &timestampCodec{}
)

// wrapperValueCodec is the codec for Protobuf type wrappers
type wrapperValueCodec struct{}

const valueTag = "Value"

// EncodeValue encodes Protobuf type wrapper value to BSON value
func (e *wrapperValueCodec) EncodeValue(ectx bsoncodec.EncodeContext, vw bsonrw.ValueWriter, val reflect.Value) error {
	val = val.FieldByName(valueTag)
	enc, err := ectx.LookupEncoder(val.Type())
	if err != nil {
		return err
	}
	return enc.EncodeValue(ectx, vw, val)
}

// DecodeValue decodes BSON value to Protobuf type wrapper value
func (e *wrapperValueCodec) DecodeValue(ectx bsoncodec.DecodeContext, vr bsonrw.ValueReader, val reflect.Value) error {
	val = val.FieldByName(valueTag)
	enc, err := ectx.LookupDecoder(val.Type())
	if err != nil {
		return err
	}
	return enc.DecodeValue(ectx, vr, val)
}

// timestampCodec is codec for Protobuf Timestamp
type timestampCodec struct{}

// EncodeValue encodes Protobuf Timestamp value to BSON value
func (e *timestampCodec) EncodeValue(ectx bsoncodec.EncodeContext, vw bsonrw.ValueWriter, val reflect.Value) error {
	if !val.CanAddr() {
		return errors.New("value is not timestamp addr")
	}
	v, ok := val.Addr().Interface().(*timestamppb.Timestamp)
	if !ok {
		return errors.New("value is not *timestamppb.Timestamp")
	}
	t := v.AsTime()
	enc, err := ectx.LookupEncoder(timeType)
	if err != nil {
		return err
	}
	return enc.EncodeValue(ectx, vw, reflect.ValueOf(t.In(time.UTC)))
}

// DecodeValue decodes BSON value to Timestamp value
func (e *timestampCodec) DecodeValue(ectx bsoncodec.DecodeContext, vr bsonrw.ValueReader, val reflect.Value) error {
	enc, err := ectx.LookupDecoder(timeType)
	if err != nil {
		return err
	}
	var t time.Time
	if err = enc.DecodeValue(ectx, vr, reflect.ValueOf(&t).Elem()); err != nil {
		return err
	}
	ts := timestamppb.New(t.In(time.UTC))
	val.Set(reflect.ValueOf(ts).Elem())
	return nil
}

// DefaultRegistry is the default bsoncodec.Registry. It contains the default codecs.
var DefaultRegistry = NewRegistry()

// EncodeToDocument any data to bson.D
func EncodeToDocument(val any) (bson.D, error) {
	return EncodeToDocumentByRegistry(val, DefaultRegistry)
}

// EncodeToDocumentByRegistry any data to bson.D
func EncodeToDocumentByRegistry(val any, r *bsoncodec.Registry) (bson.D, error) {
	buf := &bytes.Buffer{}
	vw, err := bsonrw.NewBSONValueWriter(buf)
	if err != nil {
		return nil, err
	}
	enc, errEncoder := bson.NewEncoder(vw)
	if errEncoder != nil {
		return nil, errEncoder
	}
	err = enc.SetRegistry(r)
	if err != nil {
		return nil, err
	}
	enc.UseJSONStructTags()
	enc.OmitZeroStruct()
	enc.NilMapAsEmpty()
	err = enc.Encode(val)
	if err != nil {
		return nil, err
	}
	dec, err := bson.NewDecoder(bsonrw.NewBSONDocumentReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}
	dec.UseJSONStructTags()
	dec.DefaultDocumentD()
	dec.DefaultDocumentM()
	var data bson.D
	err = dec.Decode(&data)
	if err != nil {
		return nil, err
	}
	return data, err
}

// NewRegistry the register with grpc supported
func NewRegistry() *bsoncodec.Registry {
	reg := bson.NewRegistry()
	// Encoders
	reg.RegisterTypeEncoder(boolValueType, wrapperValueCodecRef)
	reg.RegisterTypeEncoder(bytesValueType, wrapperValueCodecRef)
	reg.RegisterTypeEncoder(doubleValueType, wrapperValueCodecRef)
	reg.RegisterTypeEncoder(floatValueType, wrapperValueCodecRef)
	reg.RegisterTypeEncoder(int32ValueType, wrapperValueCodecRef)
	reg.RegisterTypeEncoder(int64ValueType, wrapperValueCodecRef)
	reg.RegisterTypeEncoder(stringValueType, wrapperValueCodecRef)
	reg.RegisterTypeEncoder(uint32ValueType, wrapperValueCodecRef)
	reg.RegisterTypeEncoder(uint64ValueType, wrapperValueCodecRef)
	reg.RegisterTypeEncoder(timestampType, timestampCodecRef)

	// Decoders
	reg.RegisterTypeDecoder(boolValueType, wrapperValueCodecRef)
	reg.RegisterTypeDecoder(bytesValueType, wrapperValueCodecRef)
	reg.RegisterTypeDecoder(doubleValueType, wrapperValueCodecRef)
	reg.RegisterTypeDecoder(floatValueType, wrapperValueCodecRef)
	reg.RegisterTypeDecoder(int32ValueType, wrapperValueCodecRef)
	reg.RegisterTypeDecoder(int64ValueType, wrapperValueCodecRef)
	reg.RegisterTypeDecoder(stringValueType, wrapperValueCodecRef)
	reg.RegisterTypeDecoder(uint32ValueType, wrapperValueCodecRef)
	reg.RegisterTypeDecoder(uint64ValueType, wrapperValueCodecRef)
	reg.RegisterTypeDecoder(timestampType, timestampCodecRef)

	return reg
}
