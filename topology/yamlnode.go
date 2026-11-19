package topology

import (
	"fmt"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/vectorihub/netctl/types"
)

// maxExpandedNodes bounds how big a document may become once aliases are
// followed. A "billion laughs" file is a few hundred bytes that expand to
// billions of nodes; counting the expansion before reading anything refuses it
// in microseconds. A real topology of thousands of machines is far below this.
const maxExpandedNodes = 500_000

// expandedNodes returns how many nodes root has once every alias is replaced by
// what it points at, stopping early once the count passes maxExpandedNodes. loop
// is the node where an anchor contains an alias to itself, or nil.
func expandedNodes(root *yaml.Node) (count uint64, loop *yaml.Node) {
	memo := map[*yaml.Node]uint64{}
	inProgress := map[*yaml.Node]bool{}
	var size func(node *yaml.Node) uint64
	var cyclic *yaml.Node
	size = func(node *yaml.Node) uint64 {
		if node == nil {
			return 0
		}
		if done, seen := memo[node]; seen {
			return done
		}
		if inProgress[node] {
			cyclic = node
			return 1
		}
		inProgress[node] = true
		total := uint64(1)
		if node.Kind == yaml.AliasNode {
			total += size(node.Alias)
		}
		for _, child := range node.Content {
			total += size(child)
			if total > maxExpandedNodes || cyclic != nil {
				total = maxExpandedNodes + 1
				break
			}
		}
		inProgress[node] = false
		memo[node] = total
		return total
	}
	count = size(root)
	return count, cyclic
}

// reader collects every problem found while turning a YAML tree into a
// Topology, so one run reports them all.
type reader struct {
	name     string
	problems []located
}

type located struct {
	line    int
	problem *types.Error
}

// fail records a problem at a line. The subject is "name:line".
func (r *reader) fail(line int, format string, args ...any) {
	subject := r.name
	if line > 0 {
		subject = fmt.Sprintf("%s:%d", r.name, line)
	}
	r.problems = append(r.problems, located{line, types.Errf(types.ErrInvalidTopology, subject, format, args...)})
}

// list returns the problems sorted by line, in the order found within a line.
func (r *reader) list() types.List {
	sort.SliceStable(r.problems, func(i, j int) bool { return r.problems[i].line < r.problems[j].line })
	var out types.List
	for _, p := range r.problems {
		out.Add(p.problem)
	}
	return out
}

// resolve follows an alias to the node it stands for.
func resolve(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.AliasNode && node.Alias != nil {
		return node.Alias
	}
	return node
}

// fields is a mapping that has passed the unknown-field and duplicate checks.
type fields struct {
	r    *reader
	path string
	node *yaml.Node // the mapping, for the line of a missing field
	by   map[string]*yaml.Node
}

// mapping checks that node is a mapping whose keys are all in allowed. It
// reports unknown keys (with the closest allowed key as a hint), keys that are
// not plain text, and keys written twice. ok is false when node is not a mapping.
func (r *reader) mapping(node *yaml.Node, path string, allowed ...string) (fields, bool) {
	target := resolve(node)
	if target == nil || target.Kind != yaml.MappingNode {
		r.fail(lineOf(node), "%s: expected a mapping (key: value pairs)", path)
		return fields{}, false
	}
	out := fields{r: r, path: path, node: node, by: map[string]*yaml.Node{}}
	firstLine := map[string]int{}
	for i := 0; i+1 < len(target.Content); i += 2 {
		key, value := target.Content[i], target.Content[i+1]
		if key.Kind != yaml.ScalarNode {
			r.fail(key.Line, "%s: a field name must be plain text", path)
			continue
		}
		name := key.Value
		if !contains(allowed, name) {
			r.fail(key.Line, "%s: unknown field %q%s", path, name, hint(name, allowed))
			continue
		}
		if first, dup := firstLine[name]; dup {
			r.fail(key.Line, "%s: field %q is given twice (first on line %d)", path, name, first)
			continue
		}
		firstLine[name] = key.Line
		out.by[name] = value
	}
	return out, true
}

// contains reports whether want is one of list.
func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// hint suggests the closest allowed field, or lists them all if none is close.
func hint(got string, allowed []string) string {
	best, bestDistance := "", 1<<30
	for _, candidate := range allowed {
		if d := editDistance(got, candidate); d < bestDistance {
			best, bestDistance = candidate, d
		}
	}
	if bestDistance <= 2 && bestDistance < len(got) {
		return fmt.Sprintf("; did you mean %q?", best)
	}
	sorted := append([]string(nil), allowed...)
	sort.Strings(sorted)
	return fmt.Sprintf("; the fields here are %v", sorted)
}

// editDistance is the Levenshtein distance between two short strings.
func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}

// lineOf returns the 1-based line a YAML node starts on, or 0 for a nil node.
func lineOf(node *yaml.Node) int {
	if node == nil {
		return 0
	}
	return node.Line
}

// has reports whether key was given.
func (f fields) has(key string) bool { _, ok := f.by[key]; return ok }

// text reads a text field. A missing or empty field is an error when required,
// and "" otherwise. Any plain scalar is accepted, so `id: 123` is the text "123".
func (f fields) text(key string, required bool) string {
	node, given := f.by[key]
	if !given {
		if required {
			f.r.fail(lineOf(f.node), "%s: missing required field %q", f.path, key)
		}
		return ""
	}
	target := resolve(node)
	if target.Kind != yaml.ScalarNode {
		f.r.fail(node.Line, "%s.%s: expected text", f.path, key)
		return ""
	}
	if target.ShortTag() == "!!null" || target.Value == "" {
		if required {
			f.r.fail(node.Line, "%s.%s: must not be empty", f.path, key)
		}
		return ""
	}
	return target.Value
}

// integer reads a whole-number field. present is false when it was not given.
func (f fields) integer(key string) (value int, present bool) {
	node, given := f.by[key]
	if !given {
		return 0, false
	}
	target := resolve(node)
	if target.Kind != yaml.ScalarNode || target.ShortTag() != "!!int" {
		f.r.fail(node.Line, "%s.%s: expected a whole number", f.path, key)
		return 0, false
	}
	parsed, err := strconv.Atoi(target.Value)
	if err != nil {
		f.r.fail(node.Line, "%s.%s: %q is not a usable whole number", f.path, key, target.Value)
		return 0, false
	}
	return parsed, true
}

// boolean reads a true/false field.
func (f fields) boolean(key string) bool {
	node, given := f.by[key]
	if !given {
		return false
	}
	target := resolve(node)
	if target.Kind != yaml.ScalarNode || target.ShortTag() != "!!bool" {
		f.r.fail(node.Line, "%s.%s: expected true or false", f.path, key)
		return false
	}
	return target.Value == "true"
}

// sequence returns the items of a list field. A missing or empty (null) field is
// an empty list; anything else that is not a list is an error.
func (f fields) sequence(key string) []*yaml.Node {
	node, given := f.by[key]
	if !given {
		return nil
	}
	return f.r.items(node, f.path+"."+key)
}

// items returns the entries of a list node, treating null as an empty list.
func (r *reader) items(node *yaml.Node, path string) []*yaml.Node {
	target := resolve(node)
	switch {
	case target.Kind == yaml.ScalarNode && target.ShortTag() == "!!null":
		return nil
	case target.Kind != yaml.SequenceNode:
		r.fail(node.Line, "%s: expected a list", path)
		return nil
	}
	return target.Content
}

// scalarText returns the text of a plain scalar list entry, or reports an error.
func (r *reader) scalarText(node *yaml.Node, path string) (string, bool) {
	target := resolve(node)
	if target.Kind != yaml.ScalarNode || target.ShortTag() == "!!null" {
		r.fail(node.Line, "%s: expected plain text", path)
		return "", false
	}
	return target.Value, true
}
