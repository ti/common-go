package uri

import (
	"fmt"
	"sort"
	"strings"
)

// decodeToNodeRoot converts the labels to a tree of nodes.
func decodeToNodeRoot(labels map[string]string, rootName string) (*node, error) {
	sortedKeys := sortKeys(labels)
	var n *node
	for i, key := range sortedKeys {
		var split []string
		if strings.Contains(key, "[") {
			split = []string{key}
		} else {
			split = strings.Split(key, ".")
		}
		if rootName != "" && split[0] != rootName {
			return nil, fmt.Errorf("invalid label root %s", split[0])
		}
		var parts []string
		for _, v := range split {
			if v == "" {
				return nil, fmt.Errorf("invalid element: %s", key)
			}
			if v[0] == '[' {
				return nil, fmt.Errorf("invalid leading character '[' in field name (bracket is a slice delimiter): %s", v)
			}
			if strings.HasSuffix(v, "]") && v[0] != '[' {
				indexLeft := strings.Index(v, "[")
				parts = append(parts, v[:indexLeft], v[indexLeft:][1:len(v[indexLeft:])-1])
			} else {
				parts = append(parts, v)
			}
		}

		if i == 0 {
			n = &node{}
		}
		if rootName == "" {
			parts = append([]string{""}, parts...)
		}
		decodeTonode(n, parts, labels[key])
	}

	return n, nil
}

func decodeTonode(root *node, path []string, value string) {
	if root.Name == "" {
		root.Name = path[0]
	}

	// it's a leaf or not -> children
	if len(path) > 1 {
		if n := containsnode(root.Children, path[1]); n != nil {
			// the child already exists
			decodeTonode(n, path[1:], value)
		} else {
			// new child
			child := &node{Name: path[1]}
			decodeTonode(child, path[1:], value)
			root.Children = append(root.Children, child)
		}
	} else {
		root.Value = value
	}
}

func containsnode(nodes []*node, name string) *node {
	for _, n := range nodes {
		if strings.EqualFold(name, n.Name) {
			return n
		}
	}
	return nil
}

func sortKeys(labels map[string]string) (sortedKeys []string) {
	for key := range labels {
		sortedKeys = append(sortedKeys, key)
		continue
	}
	sort.Strings(sortedKeys)
	return
}
