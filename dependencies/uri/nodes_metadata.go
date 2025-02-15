package uri

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// metadataOpts Options for the metadata.
type metadataOpts struct {
	TagName            string
	AllowSliceAsStruct bool
}

// addMetadata adds metadata such as type, inferred from element, to a node.
func addMetadata(element any, n *node, opts metadataOpts) error {
	return metadata{metadataOpts: opts}.Add(element, n)
}

type metadata struct {
	metadataOpts
}

// Add adds metadata such as type, inferred from element, to a node.
func (m metadata) Add(element any, node *node) error {
	if node == nil {
		return nil
	}

	if len(node.Children) == 0 {
		return fmt.Errorf("invalid node %s: no child", node.Name)
	}

	if element == nil {
		return errors.New("nil structure")
	}

	rootType := reflect.TypeOf(element)
	node.Kind = rootType.Kind()

	return m.browseChildren(rootType, node)
}

func (m metadata) browseChildren(fType reflect.Type, node *node) error {
	for _, child := range node.Children {
		if err := m.add(fType, child); err != nil {
			return err
		}
	}
	return nil
}

func (m metadata) add(rootType reflect.Type, node *node) error {
	rType := rootType
	if rootType.Kind() == reflect.Ptr {
		rType = rootType.Elem()
	}

	if rType.Kind() == reflect.Map && rType.Elem().Kind() == reflect.Interface {
		addRawValue(node)
		return nil
	}

	field, err := m.findTypedField(rType, node)
	if err != nil {
		return err
	}

	if errSupported := isSupportedType(&field); errSupported != nil {
		return errSupported
	}

	fType := field.Type
	node.Kind = fType.Kind()
	node.Tag = field.Tag

	err = m.addStruct(fType, &field, node)
	if err != nil {
		return err
	}

	if len(node.Children) == 0 {
		return nil
	}

	if fType.Kind() == reflect.Struct || fType.Kind() == reflect.Ptr && fType.Elem().Kind() == reflect.Struct {
		return m.browseChildren(fType, node)
	}
	return m.addMapAndSlice(fType, &field, node)
}

func (m metadata) addStruct(fType reflect.Type, field *reflect.StructField, node *node) error {
	if fType.Kind() == reflect.Struct || fType.Kind() == reflect.Ptr && fType.Elem().Kind() == reflect.Struct ||
		fType.Kind() == reflect.Map {
		if len(node.Children) == 0 && field.Tag.Get(m.TagName) != TagLabelAllowEmpty {
			return fmt.Errorf("%s cannot be a standalone element (type %s)", node.Name, fType)
		}

		node.Disabled = len(node.Value) > 0 && !strings.EqualFold(node.Value, "true") &&
			field.Tag.Get(m.TagName) == TagLabelAllowEmpty
	}
	return nil
}

func (m metadata) addMapAndSlice(fType reflect.Type, field *reflect.StructField, node *node) (err error) {
	if fType.Kind() == reflect.Map {
		if fType.Elem().Kind() == reflect.Interface {
			addRawValue(node)
			return nil
		}

		for _, child := range node.Children {
			// elem is a map entry value type
			elem := fType.Elem()
			child.Kind = elem.Kind()

			if elem.Kind() == reflect.Map || elem.Kind() == reflect.Struct ||
				(elem.Kind() == reflect.Ptr && elem.Elem().Kind() == reflect.Struct) {
				if err := m.browseChildren(elem, child); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if fType.Kind() == reflect.Slice {
		if m.AllowSliceAsStruct && field.Tag.Get(TagLabelSliceAsStruct) != "" {
			return m.browseChildren(fType.Elem(), node)
		}

		for _, ch := range node.Children {
			ch.Kind = fType.Elem().Kind()
			if err := m.browseChildren(fType.Elem(), ch); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("invalid node %s: %v", node.Name, fType.Kind())
}

func (m metadata) findTypedField(rType reflect.Type, node *node) (reflect.StructField, error) {
	if rType.Kind() != reflect.Struct {
		return reflect.StructField{}, fmt.Errorf("field not found, node: %s", node.Name)
	}

	for i := 0; i < rType.NumField(); i++ {
		cField := rType.Field(i)

		fieldName := cField.Tag.Get(TagLabelSliceAsStruct)
		if !m.AllowSliceAsStruct || fieldName == "" {
			fieldName = cField.Name
		}

		if isExported(&cField) {
			if cField.Anonymous {
				if cField.Type.Kind() == reflect.Struct {
					structField, err := m.findTypedField(cField.Type, node)
					if err != nil {
						continue
					}
					return structField, nil
				}
			}

			if strings.EqualFold(fieldName, node.Name) {
				node.FieldName = cField.Name
				return cField, nil
			}
		}
	}

	return reflect.StructField{}, fmt.Errorf("field not found, node: %s", node.Name)
}

// isExported reports whether f is exported.
// https://golang.org/pkg/reflect/#StructField
func isExported(f *reflect.StructField) bool {
	return f.PkgPath == ""
}

func isSupportedType(field *reflect.StructField) error {
	fType := field.Type
	if fType.Kind() == reflect.Slice {
		switch fType.Elem().Kind() {
		case reflect.String,
			reflect.Bool,
			reflect.Int,
			reflect.Int8,
			reflect.Int16,
			reflect.Int32,
			reflect.Int64,
			reflect.Uint,
			reflect.Uint8,
			reflect.Uint16,
			reflect.Uint32,
			reflect.Uint64,
			reflect.Uintptr,
			reflect.Float32,
			reflect.Float64,
			reflect.Struct,
			reflect.Ptr:
			return nil
		default:
			return fmt.Errorf("unsupported slice type: %v", fType)
		}
	}

	if fType.Kind() == reflect.Map && fType.Key().Kind() != reflect.String {
		return fmt.Errorf("unsupported map key type: %v", fType.Key())
	}

	if fType.Kind() == reflect.Func {
		return fmt.Errorf("unsupported type: %v", fType)
	}

	return nil
}

/*
RawMap section
*/

func addRawValue(node *node) {
	if node.RawValue == nil {
		node.RawValue = nodeToRawMap(node)
	}

	node.Children = nil
}

func nodeToRawMap(node *node) map[string]any {
	result := map[string]any{}

	squashnode(node, result, true)

	return result
}

func squashnode(node *node, acc map[string]any, root bool) {
	if len(node.Children) == 0 {
		acc[node.Name] = node.Value

		return
	}

	// slice
	if isArrayKey(node.Children[0].Name) {
		var accChild []any

		for _, child := range node.Children {
			tmp := map[string]any{}
			squashnode(child, tmp, false)
			accChild = append(accChild, tmp[child.Name])
		}

		acc[node.Name] = accChild

		return
	}

	// map
	var accChild map[string]any
	if root {
		accChild = acc
	} else {
		accChild = typedRawMap(acc, node.Name)
	}

	for _, child := range node.Children {
		squashnode(child, accChild, false)
	}
}

func typedRawMap(m map[string]any, k string) map[string]any {
	if m[k] == nil {
		m[k] = map[string]any{}
	}

	r, ok := m[k].(map[string]any)
	if !ok {
		panic(fmt.Sprintf("unsupported value (key: %s): %T", k, m[k]))
	}

	return r
}

func isArrayKey(name string) bool {
	return name[0] == '[' && name[len(name)-1] == ']'
}
