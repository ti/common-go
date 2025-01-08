// Package logging implements grpc logging middleware.
package logging

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pref "google.golang.org/protobuf/reflect/protoreflect"
)

type bodyEncoder struct {
	maskFields map[pref.Name]bool
}

// Encode the encode
func (b *bodyEncoder) Encode(val any, clearBytes bool) (data string) {
	if bodyProto, ok := val.(proto.Message); ok {
		if clearBytes {
			bodyProto = proto.Clone(bodyProto)
			b.clearMessageBytes(bodyProto.ProtoReflect())
		}
		b, err := protojson.Marshal(bodyProto)
		if err != nil {
			data = fmt.Sprintf("error: %s", err)
		} else {
			if len(b) > 2048 {
				b = b[:2048]
			}
			data = string(b)
		}
	} else {
		data = fmt.Sprintf("error: type %T", val)
	}
	return
}

func (b *bodyEncoder) clearMessageBytes(m pref.Message) {
	m.Range(func(fd pref.FieldDescriptor, val pref.Value) bool {
		b.clearValueBytes(m, val, fd)
		return true
	})
}

func (b *bodyEncoder) clearValueBytes(m pref.Message, val pref.Value, fd pref.FieldDescriptor) {
	switch {
	case fd.IsList():
		list := val.List()
		for i := 0; i < list.Len(); i++ {
			item := list.Get(i)
			b.clearMessageBytes(item.Message())
		}
	case fd.IsMap():
		mmap := val.Map()
		mmap.Range(func(k pref.MapKey, v pref.Value) bool {
			b.clearSingularBytes(m, v, fd.MapValue())
			return true
		})
	default:
		b.clearSingularBytes(m, val, fd)
	}
}

func (b *bodyEncoder) clearSingularBytes(m pref.Message, val pref.Value, fd pref.FieldDescriptor) {
	if !val.IsValid() {
		return
	}
	switch kind := fd.Kind(); kind {
	case pref.BytesKind:
		m.Clear(fd)
	case pref.MessageKind, pref.GroupKind:
		b.clearMessageBytes(val.Message())
	case pref.StringKind:
		if b.maskFields[fd.Name()] {
			m.Set(fd, pref.ValueOfString("*"))
		}
	}
}
