// Package jsonschema is a small JSON Schema validator for the one schema netctl
// publishes, so that the validator can check every generated config against it
// without a dependency (only yaml.v3 is allowed).
//
// It supports exactly the keywords the NodeConfig schema uses: type (string, boolean,
// array, object, integer), const, enum, pattern, minimum, maximum, minLength,
// properties, required, additionalProperties (as a boolean), items, uniqueItems and
// local $ref. [Schema.UnsupportedKeywords] lists any other keyword a schema uses, and
// a test insists there is none, so a keyword added to the schema cannot be silently
// ignored.
package jsonschema

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// supported lists the keywords the validator understands, and the ones that only
// describe the schema.
var supported = map[string]bool{
	"$schema": true, "$id": true, "title": true, "description": true, "$defs": true,
	"type": true, "const": true, "enum": true, "pattern": true, "minimum": true, "maximum": true, "minLength": true,
	"properties": true, "required": true, "additionalProperties": true, "items": true, "uniqueItems": true, "$ref": true,
}

// Schema is a JSON Schema ready to check documents against.
//
// Example:
//
//	s, err := jsonschema.New(config.SchemaJSON()); problems, err := s.Validate(doc)
type Schema struct {
	// root is the whole schema, where $ref points into.
	root map[string]any
}

// New parses a schema. It returns an error if the schema is not JSON.
//
// Example:
//
//	s, err := jsonschema.New(config.SchemaJSON())
func New(schema []byte) (*Schema, error) {
	var root map[string]any
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil, err
	}
	return &Schema{root: root}, nil
}

// UnsupportedKeywords returns every keyword in the schema the validator does not
// understand, with where it is, sorted. A schema that uses one would be checked less
// strictly than it reads, so a test should insist the list is empty.
//
// Example:
//
//	jsonschema.New(schema) then s.UnsupportedKeywords() is empty for the NodeConfig schema.
func (v *Schema) UnsupportedKeywords() []string {
	var out []string
	var rec func(path string, node any)
	rec = func(path string, node any) {
		switch n := node.(type) {
		case map[string]any:
			for k, child := range n {
				switch {
				case path == "properties" || path == "$defs":
					rec(path+"."+k, child) // names of properties and definitions, not keywords
				case !supported[k]:
					out = append(out, path+"/"+k)
				default:
					rec(k, child)
				}
			}
		case []any:
			for _, child := range n {
				rec(path, child)
			}
		}
	}
	rec("", v.root)
	sort.Strings(out)
	return out
}

// Validate checks the JSON document against the schema and returns every violation,
// each with its path, sorted. It returns an error only if the document is not JSON.
//
// Example:
//
//	problems, err := s.Validate(data); len(problems) == 0 means it is valid.
func (v *Schema) Validate(doc []byte) ([]string, error) {
	var value any
	if err := json.Unmarshal(doc, &value); err != nil {
		return nil, err
	}
	var problems []string
	v.check(v.root, value, "$", &problems)
	sort.Strings(problems)
	return problems, nil
}

// resolve follows a local $ref such as "#/$defs/id".
func (v *Schema) resolve(ref string) map[string]any {
	node := any(v.root)
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, _ := node.(map[string]any)
		node = m[part]
	}
	out, _ := node.(map[string]any)
	return out
}

// check validates one value against one schema node.
func (v *Schema) check(schema map[string]any, value any, path string, problems *[]string) {
	add := func(format string, args ...any) {
		*problems = append(*problems, path+": "+fmt.Sprintf(format, args...))
	}
	if ref, ok := schema["$ref"].(string); ok {
		v.check(v.resolve(ref), value, path, problems)
		return
	}
	if want, ok := schema["const"]; ok && value != want {
		add("must be %v", want)
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			found = found || e == value
		}
		if !found {
			add("%v is not one of %v", value, enum)
		}
	}
	if typ, ok := schema["type"].(string); ok && !typeMatches(typ, value) {
		add("must be %s, got %T", typ, value)
		return
	}
	switch val := value.(type) {
	case string:
		if pattern, ok := schema["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(val) {
			add("%q does not match %s", val, pattern)
		}
		if min, ok := schema["minLength"].(float64); ok && float64(len(val)) < min {
			add("is shorter than %v", min)
		}
	case float64:
		if min, ok := schema["minimum"].(float64); ok && val < min {
			add("%v is below %v", val, min)
		}
		if max, ok := schema["maximum"].(float64); ok && val > max {
			add("%v is above %v", val, max)
		}
	case []any:
		if item, ok := schema["items"].(map[string]any); ok {
			for i, e := range val {
				v.check(item, e, fmt.Sprintf("%s[%d]", path, i), problems)
			}
		}
		if unique, _ := schema["uniqueItems"].(bool); unique {
			seen := map[string]bool{}
			for _, e := range val {
				key := fmt.Sprint(e)
				if seen[key] {
					add("has %v twice", e)
				}
				seen[key] = true
			}
		}
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		if req, ok := schema["required"].([]any); ok {
			for _, name := range req {
				if _, present := val[name.(string)]; !present {
					add("is missing %q", name)
				}
			}
		}
		for name, child := range val {
			if sub, known := props[name].(map[string]any); known {
				v.check(sub, child, path+"."+name, problems)
			} else if extra, ok := schema["additionalProperties"].(bool); ok && !extra {
				add("has the key %q, which the schema does not allow", name)
			}
		}
	}
}

// typeMatches reports whether a decoded JSON value has the schema type.
func typeMatches(typ string, value any) bool {
	switch typ {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "integer":
		f, ok := value.(float64)
		return ok && f == math.Trunc(f)
	}
	return false
}
