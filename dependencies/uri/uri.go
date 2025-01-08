package uri

import (
	"errors"
	"net/url"
	"reflect"
	"strings"
)

// Unmarshal parses the URL-encoded data and stores the result.
func Unmarshal(uri *url.URL, v any) error {
	target := reflect.ValueOf(v)
	if target.Kind() != reflect.Ptr {
		return errors.New("dependencies must be pointer")
	}
	e := target.Elem()
	tagsMap := map[string]bool{}
	for i := 0; i < e.NumField(); i++ {
		f := e.Type().Field(i)
		tagsMap[strings.ToLower(f.Name)] = true
	}
	data := map[string]string{}
	if tagsMap["scheme"] {
		data["scheme"] = uri.Scheme
	}
	if tagsMap["host"] {
		data["host"] = uri.Host
	}
	if user := uri.User; user != nil {
		if tagsMap["username"] {
			data["username"] = user.Username()
		}
		if tagsMap["password"] {
			password, _ := uri.User.Password()
			if password != "" {
				data["password"] = password
			}
		}
	}
	if len(uri.Path) > 1 && tagsMap["namespace"] {
		data["namespace"] = uri.Path[1:]
	}
	for k, v := range uri.Query() {
		if len(v) == 0 {
			data[k] = ""
		} else {
			data[k] = v[0]
		}
	}
	return DecodeMap(data, v, "")
}

// DecodeQuery decode url query as a object.
func DecodeQuery(query url.Values, v any) error {
	data := map[string]string{}
	for k, v := range query {
		if len(v) == 0 {
			data[k] = ""
		} else {
			data[k] = v[0]
		}
	}
	return DecodeMap(data, v, "")
}

// DecodeMap decodes the given map of labels into the given element.
func DecodeMap(labels map[string]string, element any, rootName string) error {
	node, err := decodeToNodeRoot(labels, rootName)
	if err != nil {
		return err
	}
	metaOpts := metadataOpts{TagName: TagLabel, AllowSliceAsStruct: true}
	err = addMetadata(element, node, metaOpts)
	if err != nil {
		return err
	}

	return fill(element, node, fillerOpts{AllowSliceAsStruct: true})
}
