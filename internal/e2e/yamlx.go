package e2e

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Edit is one change to a YAML fixture, applied to the parsed document so comments and key
// order elsewhere in the file survive.
type Edit func(root *yaml.Node) error

// Set replaces the value at a path with a Go value. The path uses dots, indices and
// [field=value] selectors, and a link can be selected by its computed ID:
// links[id=builder-1~dgx-spark].cost. Setting a key that does not exist adds it.
//
// Example:
//
//	w.Edit("topo.yaml", e2e.Set("links[id=builder-1~dgx-spark].cost", 50))
func Set(path string, value any) Edit {
	return func(root *yaml.Node) error {
		parent, key, err := locate(root, path)
		if err != nil {
			return err
		}
		var node yaml.Node
		if err := node.Encode(value); err != nil {
			return err
		}
		switch parent.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(parent.Content); i += 2 {
				if parent.Content[i].Value == key {
					parent.Content[i+1] = &node
					return nil
				}
			}
			parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &node)
			return nil
		case yaml.SequenceNode:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(parent.Content) {
				return fmt.Errorf("set %s: bad index %q", path, key)
			}
			parent.Content[i] = &node
			return nil
		}
		return fmt.Errorf("set %s: parent is not a map or a list", path)
	}
}

// Append adds a Go value to the end of the list at the path.
//
// Example:
//
//	w.Edit("topo.yaml", e2e.Append("nodes", map[string]any{"id": "mac-studio", "site": "studio", "role": "spoke"}))
func Append(path string, value any) Edit {
	return func(root *yaml.Node) error {
		list, err := resolve(root, path)
		if err != nil {
			return err
		}
		if list.Kind != yaml.SequenceNode {
			return fmt.Errorf("append %s: not a list", path)
		}
		var node yaml.Node
		if err := node.Encode(value); err != nil {
			return err
		}
		list.Content = append(list.Content, &node)
		return nil
	}
}

// Delete removes the list element or map key at the path.
//
// Example:
//
//	w.Edit("topo.yaml", e2e.Delete("nodes[id=mac-studio]"))
func Delete(path string) Edit {
	return func(root *yaml.Node) error {
		parent, key, err := locate(root, path)
		if err != nil {
			return err
		}
		switch parent.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(parent.Content); i += 2 {
				if parent.Content[i].Value == key {
					parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
					return nil
				}
			}
		case yaml.SequenceNode:
			if i, err := strconv.Atoi(key); err == nil && i >= 0 && i < len(parent.Content) {
				parent.Content = append(parent.Content[:i], parent.Content[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("delete %s: not found", path)
	}
}

// Shuffle reorders the lists at the paths with a seeded shuffle. It refuses "intents",
// because the order of intents is meaningful.
//
// Example:
//
//	w.Edit("topo.yaml", e2e.Shuffle(7, "sites", "nodes", "links"))
func Shuffle(seed int64, paths ...string) Edit {
	return func(root *yaml.Node) error {
		rng := rand.New(rand.NewPCG(uint64(seed), 0x6e657463746c))
		for _, path := range paths {
			if path == "intents" {
				return fmt.Errorf("shuffle: intents are ordered and must not be shuffled")
			}
			list, err := resolve(root, path)
			if err != nil {
				return err
			}
			if list.Kind != yaml.SequenceNode {
				return fmt.Errorf("shuffle %s: not a list", path)
			}
			rng.Shuffle(len(list.Content), func(i, j int) { list.Content[i], list.Content[j] = list.Content[j], list.Content[i] })
		}
		return nil
	}
}

// Edit applies edits to a workspace YAML file in order and writes the result back. An edit
// that cannot apply fails the test.
func (w *W) Edit(file string, edits ...Edit) {
	w.T.Helper()
	out, err := EditBytes(w.Read(file), edits...)
	if err != nil {
		w.Fatalf("edit %s: %v", file, err)
	}
	w.Write(file, out)
}

// EditBytes applies edits to YAML text and returns the new text.
func EditBytes(data []byte, edits ...Edit) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, fmt.Errorf("not a single YAML document")
	}
	for _, edit := range edits {
		if err := edit(doc.Content[0]); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

// locate resolves everything but the last part of a path and returns the parent node and
// the last part, so Set and Delete can act on it.
func locate(root *yaml.Node, path string) (*yaml.Node, string, error) {
	parts := splitPath(path)
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("empty path")
	}
	last := parts[len(parts)-1]
	if strings.HasPrefix(last, "[") {
		// A selector as the last part names an element of the list before it.
		list, err := walkNodes(root, parts[:len(parts)-1])
		if err != nil {
			return nil, "", err
		}
		sel, err := selectNode(list, last)
		if err != nil {
			return nil, "", err
		}
		for i, item := range list.Content {
			if item == sel {
				return list, strconv.Itoa(i), nil
			}
		}
	}
	parent, err := walkNodes(root, parts[:len(parts)-1])
	return parent, last, err
}

// resolve returns the node a path points to.
func resolve(root *yaml.Node, path string) (*yaml.Node, error) {
	return walkNodes(root, splitPath(path))
}

// walkNodes follows path parts from a node.
func walkNodes(cur *yaml.Node, parts []string) (*yaml.Node, error) {
	for _, part := range parts {
		switch {
		case strings.HasPrefix(part, "["):
			next, err := selectNode(cur, part)
			if err != nil {
				return nil, err
			}
			cur = next
		case cur.Kind == yaml.MappingNode:
			var found *yaml.Node
			for i := 0; i+1 < len(cur.Content); i += 2 {
				if cur.Content[i].Value == part {
					found = cur.Content[i+1]
				}
			}
			if found == nil {
				return nil, fmt.Errorf("no key %q", part)
			}
			cur = found
		case cur.Kind == yaml.SequenceNode:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(cur.Content) {
				return nil, fmt.Errorf("bad index %q", part)
			}
			cur = cur.Content[i]
		default:
			return nil, fmt.Errorf("cannot step into %q", part)
		}
	}
	return cur, nil
}

// selectNode picks the list element whose field equals the value of a [field=value]
// selector. The field "id" of a link, which has none, is its computed ID, "a~b" with the
// ends in order.
func selectNode(list *yaml.Node, selector string) (*yaml.Node, error) {
	if list.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("selector %s: not a list", selector)
	}
	field, want, _ := strings.Cut(strings.Trim(selector, "[]"), "=")
	for _, item := range list.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		fields := map[string]string{}
		for i := 0; i+1 < len(item.Content); i += 2 {
			fields[item.Content[i].Value] = item.Content[i+1].Value
		}
		if _, has := fields["id"]; !has && fields["a"] != "" && fields["b"] != "" {
			a, b := fields["a"], fields["b"]
			if b < a {
				a, b = b, a
			}
			fields["id"] = a + "~" + b
		}
		if fields[field] == want {
			return item, nil
		}
	}
	return nil, fmt.Errorf("selector %s: no element", selector)
}
