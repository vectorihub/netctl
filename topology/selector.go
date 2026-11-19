package topology

import (
	"slices"
	"strings"

	"github.com/vectorihub/netctl/types"
)

// SelectorKind says how a Selector picks nodes.
type SelectorKind int

const (
	// SelectorUnknown is the zero value: no selector was set.
	SelectorUnknown SelectorKind = iota
	// SelectorAll picks every node: "*".
	SelectorAll
	// SelectorNode picks one node by ID: "node:builder-1" or just "builder-1".
	SelectorNode
	// SelectorSite picks every node in a site: "site:aws".
	SelectorSite
	// SelectorLabel picks every node carrying a label: "label:env=prod".
	SelectorLabel
)

// Selector picks nodes. It is the parsed form of the text in a topology file;
// Matches and Resolve say which nodes it picks. Policy (Phase 3) uses the same
// type and the same resolution, so "site:builder" means one thing everywhere.
type Selector struct {
	// Kind says how nodes are picked.
	Kind SelectorKind
	// ID is the node or site ID, for SelectorNode and SelectorSite.
	ID string
	// Key and Value are the label to match, for SelectorLabel.
	Key, Value string
}

// String returns the canonical text: "*", "node:ID", "site:ID" or "label:k=v".
// A bare ID in a file is written back as "node:ID".
func (selector Selector) String() string {
	switch selector.Kind {
	case SelectorAll:
		return "*"
	case SelectorNode:
		return "node:" + selector.ID
	case SelectorSite:
		return "site:" + selector.ID
	case SelectorLabel:
		return "label:" + selector.Key + "=" + selector.Value
	}
	return ""
}

// ParseSelector reads the text of a selector. It checks the syntax only: it does
// not check that anything matches (rule T10 does).
//
// The forms are "*", "node:ID", "site:ID", "label:key=value", and a bare ID,
// which means "node:ID". IDs must satisfy types.ValidID; a label needs a key
// and a value. Anything else is INVALID_TOPOLOGY.
//
// Example: ParseSelector("site:builder") returns a SelectorSite for "builder".
func ParseSelector(text string) (Selector, error) {
	if text == "*" {
		return Selector{Kind: SelectorAll}, nil
	}
	kind, rest, hasKind := strings.Cut(text, ":")
	if !hasKind {
		kind, rest = "node", text
	}
	switch kind {
	case "node", "site":
		if !types.ValidID(rest) {
			return Selector{}, types.Errf(types.ErrInvalidTopology, text, "%q is not a valid %s ID", rest, kind)
		}
		if kind == "site" {
			return Selector{Kind: SelectorSite, ID: rest}, nil
		}
		return Selector{Kind: SelectorNode, ID: rest}, nil
	case "label":
		key, value, found := strings.Cut(rest, "=")
		if !found || key == "" || value == "" || strings.ContainsAny(key+value, " \t\r\n") {
			return Selector{}, types.Errf(types.ErrInvalidTopology, text, "a label selector is label:key=value, with no spaces")
		}
		return Selector{Kind: SelectorLabel, Key: key, Value: value}, nil
	}
	return Selector{}, types.Errf(types.ErrInvalidTopology, text, "unknown selector kind %q; use *, node:ID, site:ID or label:key=value", kind)
}

// Matches reports whether the selector picks node. A selector of unknown kind
// picks nothing. It looks only at the node, so it needs no topology.
//
// Example: Selector{Kind: SelectorSite, ID: "aws"}.Matches(node) is true for any
// node whose Site is "aws".
func (selector Selector) Matches(node Node) bool {
	switch selector.Kind {
	case SelectorAll:
		return true
	case SelectorNode:
		return string(node.ID) == selector.ID
	case SelectorSite:
		return string(node.Site) == selector.ID
	case SelectorLabel:
		value, has := node.Labels[selector.Key]
		return has && value == selector.Value
	}
	return false
}

// Resolve returns the IDs of the nodes in t that the selector picks, sorted in
// byte order with no repeats, whatever order the nodes are in. It returns nil
// when nothing matches (rule T10 treats that as an error) and for a nil
// Topology. It never changes t.
//
// Example: Resolve of "site:builder" on the three-site topology returns
// ["builder-1"].
func (selector Selector) Resolve(t *Topology) []types.NodeID {
	if t == nil {
		return nil
	}
	var ids []types.NodeID
	for _, node := range t.Nodes {
		if selector.Matches(node) {
			ids = append(ids, node.ID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}
