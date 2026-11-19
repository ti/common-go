package grpcmux

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// maskTestDoc builds the descriptor of
//
//	message Doc {
//	  string title = 1;
//	  string display_name = 2;
//	  Address address = 3;
//	  repeated string tags = 4;
//	  repeated Address places = 5;
//	  google.protobuf.Struct meta = 6;
//	  google.protobuf.Any extra = 7;
//	  google.protobuf.Timestamp publish_time = 8;
//	}
//	message Address { string city = 1; string zip_code = 2; Address parent = 3; }
func maskTestDoc(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	field := func(name string, number int32, typ descriptorpb.FieldDescriptorProto_Type, typeName string, repeated bool) *descriptorpb.FieldDescriptorProto {
		f := &descriptorpb.FieldDescriptorProto{
			Name: new(name), Number: new(number), Type: typ.Enum(),
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		}
		if typeName != "" {
			f.TypeName = new(typeName)
		}
		if repeated {
			f.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
		}
		return f
	}
	const str, msg = descriptorpb.FieldDescriptorProto_TYPE_STRING, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    new("grpcmux/masktest/v1/doc.proto"),
		Package: new("grpcmux.masktest.v1"),
		Syntax:  new("proto3"),
		Dependency: []string{
			structpb.File_google_protobuf_struct_proto.Path(),
			anypb.File_google_protobuf_any_proto.Path(),
			timestamppb.File_google_protobuf_timestamp_proto.Path(),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: new("Doc"), Field: []*descriptorpb.FieldDescriptorProto{
				field("title", 1, str, "", false),
				field("display_name", 2, str, "", false),
				field("address", 3, msg, ".grpcmux.masktest.v1.Address", false),
				field("tags", 4, str, "", true),
				field("places", 5, msg, ".grpcmux.masktest.v1.Address", true),
				field("meta", 6, msg, ".google.protobuf.Struct", false),
				field("extra", 7, msg, ".google.protobuf.Any", false),
				field("publish_time", 8, msg, ".google.protobuf.Timestamp", false),
			}},
			{Name: new("Address"), Field: []*descriptorpb.FieldDescriptorProto{
				field("city", 1, str, "", false),
				field("zip_code", 2, str, "", false),
				field("parent", 3, msg, ".grpcmux.masktest.v1.Address", false),
			}},
		},
	}
	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	return fd.Messages().ByName("Doc")
}

// The mask derived from a PATCH body names the declared fields the body
// sets, and nothing else: a member the message does not know (a newer
// client's field) is left out at every depth, as the decoder ignores it.
func TestFieldMaskFromBody(t *testing.T) {
	doc := maskTestDoc(t)
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"proto names", `{"title":"t","display_name":"d"}`, []string{"display_name", "title"}},
		{"JSON names yield proto names", `{"displayName":"d","publishTime":"2026-01-01T00:00:00Z"}`, []string{"display_name", "publish_time"}},
		{"both forms of one field, once", `{"display_name":"a","displayName":"b"}`, []string{"display_name"}},
		{"unknown top-level member is left out", `{"title":"t","futureField":1}`, []string{"title"}},
		{"only unknown members: empty mask", `{"futureField":{"a":1},"other":[1,2]}`, nil},
		{"nested message is descended into", `{"address":{"city":"x","zipCode":"1"}}`, []string{"address.city", "address.zip_code"}},
		{"unknown nested member is left out", `{"address":{"city":"x","futureField":true}}`, []string{"address.city"}},
		{"unknown member two levels down", `{"address":{"parent":{"zip_code":"1","x":{"y":1}}}}`, []string{"address.parent.zip_code"}},
		{"object with only unknown members names nothing", `{"title":"t","address":{"futureField":1}}`, []string{"title"}},
		{"empty object is a leaf (clears the field)", `{"address":{}}`, []string{"address"}},
		{"null is a leaf", `{"address":null,"title":null}`, []string{"address", "title"}},
		{"repeated fields are leaves", `{"tags":["a"],"places":[{"city":"x","futureField":1}]}`, []string{"places", "tags"}},
		{"Struct keys are path segments as they are", `{"meta":{"a":{"b":1},"c":2}}`, []string{"meta.a.b", "meta.c"}},
		{"Any is a leaf", `{"extra":{"@type":"type.googleapis.com/google.protobuf.Empty","whatever":1}}`, []string{"extra"}},
		{"empty body object", `{}`, nil},
		{"not an object", `[1,2]`, nil},
		{"not JSON", `{"title":`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fieldMaskFromBody([]byte(tt.body), doc); !slices.Equal(got, tt.want) {
				t.Errorf("fieldMaskFromBody(%s) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}
