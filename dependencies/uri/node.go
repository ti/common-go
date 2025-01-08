package uri

import "reflect"

// node is a label node.
type node struct {
	RawValue  any               `json:"rawValue,omitempty"`
	Name      string            `json:"name"`
	FieldName string            `json:"fieldName"`
	Value     string            `json:"value,omitempty"`
	Tag       reflect.StructTag `json:"tag,omitempty"`
	Children  []*node           `json:"children,omitempty"`
	Kind      reflect.Kind      `json:"kind,omitempty"`
	Disabled  bool              `json:"disabled,omitempty"`
}
