# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: proto/main.proto
# Protobuf Python Version: 5.27.1
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import runtime_version as _runtime_version
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
_runtime_version.ValidateProtobufRuntimeVersion(
    _runtime_version.Domain.PUBLIC,
    5,
    27,
    1,
    '',
    'proto/main.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from validate import validate_pb2 as validate_dot_validate__pb2
from google.api import annotations_pb2 as google_dot_api_dot_annotations__pb2
from google.protobuf import timestamp_pb2 as google_dot_protobuf_dot_timestamp__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x10proto/main.proto\x12\x02pb\x1a\x17validate/validate.proto\x1a\x1cgoogle/api/annotations.proto\x1a\x1fgoogle/protobuf/timestamp.proto\"P\n\x07Request\x12\x15\n\x04name\x18\x01 \x01(\tB\x07\xfa\x42\x04r\x02( \x12.\n\ncreated_at\x18\x07 \x01(\x0b\x32\x1a.google.protobuf.Timestamp\"\x17\n\x08Response\x12\x0b\n\x03msg\x18\x01 \x01(\t2\x86\x01\n\x03Say\x12?\n\x05Hello\x12\x0b.pb.Request\x1a\x0c.pb.Response\"\x1b\x82\xd3\xe4\x93\x02\x15\"\x10/v1/hello/{name}:\x01*\x12>\n\x0bHelloStream\x12\x0b.pb.Request\x1a\x0c.pb.Response\"\x12\x82\xd3\xe4\x93\x02\x0c\"\n/v1/stream0\x01\x42?Z=github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto;pbb\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'proto.main_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'Z=github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto;pb'
  _globals['_REQUEST'].fields_by_name['name']._loaded_options = None
  _globals['_REQUEST'].fields_by_name['name']._serialized_options = b'\372B\004r\002( '
  _globals['_SAY'].methods_by_name['Hello']._loaded_options = None
  _globals['_SAY'].methods_by_name['Hello']._serialized_options = b'\202\323\344\223\002\025\"\020/v1/hello/{name}:\001*'
  _globals['_SAY'].methods_by_name['HelloStream']._loaded_options = None
  _globals['_SAY'].methods_by_name['HelloStream']._serialized_options = b'\202\323\344\223\002\014\"\n/v1/stream'
  _globals['_REQUEST']._serialized_start=112
  _globals['_REQUEST']._serialized_end=192
  _globals['_RESPONSE']._serialized_start=194
  _globals['_RESPONSE']._serialized_end=217
  _globals['_SAY']._serialized_start=220
  _globals['_SAY']._serialized_end=354
# @@protoc_insertion_point(module_scope)
