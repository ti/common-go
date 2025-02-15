// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        v5.27.1
// source: proto/error.proto

package pb

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// ErrorCode error code enumeration type
// General error, reference: https://github.com/googleapis/googleapis/blob/master/google/rpc/code.proto
type ErrorCode int32

const (
	ErrorCode_OK ErrorCode = 0
	// If the task does not exist, the grpcmux framework will automatically output the error code of ResourceNotFound.TaskNotFound to the user.
	ErrorCode_CustomNotFound ErrorCode = 4404
)

// Enum value maps for ErrorCode.
var (
	ErrorCode_name = map[int32]string{
		0:    "OK",
		4404: "CustomNotFound",
	}
	ErrorCode_value = map[string]int32{
		"OK":             0,
		"CustomNotFound": 4404,
	}
)

func (x ErrorCode) Enum() *ErrorCode {
	p := new(ErrorCode)
	*p = x
	return p
}

func (x ErrorCode) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (ErrorCode) Descriptor() protoreflect.EnumDescriptor {
	return file_proto_error_proto_enumTypes[0].Descriptor()
}

func (ErrorCode) Type() protoreflect.EnumType {
	return &file_proto_error_proto_enumTypes[0]
}

func (x ErrorCode) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use ErrorCode.Descriptor instead.
func (ErrorCode) EnumDescriptor() ([]byte, []int) {
	return file_proto_error_proto_rawDescGZIP(), []int{0}
}

var File_proto_error_proto protoreflect.FileDescriptor

var file_proto_error_proto_rawDesc = []byte{
	0x0a, 0x11, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x12, 0x02, 0x70, 0x62, 0x2a, 0x28, 0x0a, 0x09, 0x45, 0x72, 0x72, 0x6f, 0x72,
	0x43, 0x6f, 0x64, 0x65, 0x12, 0x06, 0x0a, 0x02, 0x4f, 0x4b, 0x10, 0x00, 0x12, 0x13, 0x0a, 0x0e,
	0x43, 0x75, 0x73, 0x74, 0x6f, 0x6d, 0x4e, 0x6f, 0x74, 0x46, 0x6f, 0x75, 0x6e, 0x64, 0x10, 0xb4,
	0x22, 0x42, 0x3f, 0x5a, 0x3d, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f,
	0x74, 0x69, 0x2f, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2d, 0x67, 0x6f, 0x2f, 0x64, 0x6f, 0x63,
	0x73, 0x2f, 0x74, 0x75, 0x74, 0x6f, 0x72, 0x69, 0x61, 0x6c, 0x2f, 0x72, 0x65, 0x73, 0x74, 0x66,
	0x75, 0x6c, 0x2f, 0x70, 0x6b, 0x67, 0x2f, 0x67, 0x6f, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x3b,
	0x70, 0x62, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_proto_error_proto_rawDescOnce sync.Once
	file_proto_error_proto_rawDescData = file_proto_error_proto_rawDesc
)

func file_proto_error_proto_rawDescGZIP() []byte {
	file_proto_error_proto_rawDescOnce.Do(func() {
		file_proto_error_proto_rawDescData = protoimpl.X.CompressGZIP(file_proto_error_proto_rawDescData)
	})
	return file_proto_error_proto_rawDescData
}

var file_proto_error_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_proto_error_proto_goTypes = []any{
	(ErrorCode)(0), // 0: pb.ErrorCode
}
var file_proto_error_proto_depIdxs = []int32{
	0, // [0:0] is the sub-list for method output_type
	0, // [0:0] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_proto_error_proto_init() }
func file_proto_error_proto_init() {
	if File_proto_error_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_proto_error_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   0,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_proto_error_proto_goTypes,
		DependencyIndexes: file_proto_error_proto_depIdxs,
		EnumInfos:         file_proto_error_proto_enumTypes,
	}.Build()
	File_proto_error_proto = out.File
	file_proto_error_proto_rawDesc = nil
	file_proto_error_proto_goTypes = nil
	file_proto_error_proto_depIdxs = nil
}
