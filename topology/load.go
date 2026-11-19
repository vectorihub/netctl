package topology

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vectorihub/netctl/cidr"
	"github.com/vectorihub/netctl/types"
)

// DefaultMaxBytes is the input size limit Decode applies when it is given none:
// 5 MiB, far more than any real topology needs.
const DefaultMaxBytes = 5 << 20

// Decode reads a topology from YAML bytes. The caller reads the file; netctl
// never does. name is only used to label errors, usually the file name.
//
// Decoding is strict, so a file that could mean two things is refused instead of
// guessed at:
//   - Input over maxBytes (DefaultMaxBytes if maxBytes is 0 or less) is refused
//     before it is parsed, and so is a document whose aliases expand past half a
//     million nodes, which is how a "billion laughs" file works.
//   - An unknown field is an error that names its line and, when one is close,
//     the field you probably meant ("rol" suggests "role").
//   - A field given twice, a value of the wrong kind, an unknown role, NAT mode,
//     link kind or health mode, a bad endpoint, prefix, port, cost or selector,
//     and a missing required field are errors.
//   - A node may list "peers" instead of the file having "links"; Decode turns
//     them into links with ImportPeers and refuses a pair listed on one side only.
//   - Merge keys ("<<") are refused; plain aliases are allowed.
//   - There must be exactly one document.
//
// Every problem is returned together, as a types.List of INVALID_TOPOLOGY errors
// whose Subject is "name:line", in line order. On success the result is
// normalised (see Topology.Normalize): defaults filled in, links stored with A
// before B, and sites, nodes and links sorted by ID, so two files that describe
// the same network decode to the same Topology. Decode checks the shape of
// the file, not the network rules (see Check), so a wrong schema string or a duplicate ID is not an error here.
//
// Example: a file with "rol: hub" on line 17 fails with
// "INVALID_TOPOLOGY: three-site.yaml:17: nodes[0]: unknown field "rol"; did you mean "role"?".
func Decode(name string, data []byte, maxBytes int) (*Topology, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if len(data) > maxBytes {
		return nil, oneProblem(name, 0, "input exceeds %s limit", byteLimit(maxBytes))
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, oneProblem(name, 0, "the file is empty")
		}
		return nil, yamlProblem(name, err)
	}
	var extra yaml.Node
	switch err := decoder.Decode(&extra); {
	case err == nil:
		return nil, oneProblem(name, extra.Line, "the file holds more than one YAML document; a topology is exactly one")
	case !errors.Is(err, io.EOF):
		return nil, yamlProblem(name, err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return nil, oneProblem(name, document.Line, "the file holds no topology")
	}
	root := document.Content[0]

	count, loop := expandedNodes(root)
	switch {
	case loop != nil:
		return nil, oneProblem(name, loop.Line, "an alias refers to a node that contains it")
	case count > maxExpandedNodes:
		return nil, oneProblem(name, root.Line, "excessive aliasing: the file expands to more than %d nodes", maxExpandedNodes)
	}

	r := &reader{name: name}
	topology := r.topology(root)
	if problems := r.list(); len(problems) > 0 {
		return nil, problems
	}
	if err := topology.ImportPeers(); err != nil {
		return nil, relabel(name, err)
	}
	topology.Normalize()
	return topology, nil
}

// relabel puts the file name in front of each import problem, so the message
// says where it came from. The subject stays the link, which is what the caller
// matches on.
func relabel(name string, err error) error {
	var list types.List
	if errors.As(err, &list) {
		for i, problem := range list {
			list[i] = types.Errf(problem.Code, problem.Subject, "%s: %s", name, problem.Msg)
		}
	}
	return err
}

// oneProblem builds the error list for a failure that stops decoding at once.
func oneProblem(name string, line int, format string, args ...any) error {
	r := &reader{name: name}
	r.fail(line, format, args...)
	return r.list()
}

var (
	yamlLine       = regexp.MustCompile(`line (\d+)`)
	yamlLinePrefix = regexp.MustCompile(`^line \d+: `)
)

// yamlProblem turns a YAML syntax error into a located problem.
func yamlProblem(name string, err error) error {
	message := strings.TrimPrefix(err.Error(), "yaml: ")
	line := 0
	if match := yamlLine.FindStringSubmatch(message); match != nil {
		line, _ = strconv.Atoi(match[1])
	}
	// The line is already in the subject, so drop it from the message.
	return oneProblem(name, line, "%s", yamlLinePrefix.ReplaceAllString(message, ""))
}

// byteLimit writes a limit as "5 MiB" when it is whole mebibytes.
func byteLimit(bytes int) string {
	if bytes%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", bytes>>20)
	}
	return fmt.Sprintf("%d byte", bytes)
}

// topology reads the top-level mapping.
func (r *reader) topology(root *yaml.Node) *Topology {
	top, ok := r.mapping(root, "topology", "schema", "name", "addressing", "sites", "nodes", "links", "intents")
	if !ok {
		return nil
	}
	result := &Topology{
		Schema: top.text("schema", false),
		Name:   top.text("name", false),
	}
	if node, given := top.by["addressing"]; given {
		result.Addressing = r.addressing(node)
	}
	for i, item := range top.sequence("sites") {
		result.Sites = append(result.Sites, r.site(item, fmt.Sprintf("sites[%d]", i)))
	}
	for i, item := range top.sequence("nodes") {
		result.Nodes = append(result.Nodes, r.node(item, fmt.Sprintf("nodes[%d]", i)))
	}
	for i, item := range top.sequence("links") {
		result.Links = append(result.Links, r.link(item, fmt.Sprintf("links[%d]", i)))
	}
	for i, item := range top.sequence("intents") {
		result.Intents = append(result.Intents, r.intent(item, fmt.Sprintf("intents[%d]", i)))
	}
	return result
}

// prefix reads an IPv4 prefix with no host bits set. Host bits are an error, not
// silently masked, because the loader has no way to warn yet and a typo in a
// prefix should not quietly change which block is meant.
func (r *reader) prefix(node *yaml.Node, path, text string) netip.Prefix {
	parsed, err := netip.ParsePrefix(text)
	switch {
	case err != nil:
		r.fail(node.Line, "%s: %q is not a prefix like 10.42.0.0/16", path, text)
	case !parsed.Addr().Is4():
		r.fail(node.Line, "%s: %q is not IPv4; v1alpha1 addresses are IPv4", path, text)
	default:
		if canonical, changed := cidr.Canonical(parsed); changed {
			r.fail(node.Line, "%s: %q has host bits set; write %s", path, text, canonical)
			return netip.Prefix{}
		}
		return parsed
	}
	return netip.Prefix{}
}

// addressing reads the addressing block: either "$ref" naming a separate plan
// file, or an inline plan with a supernet and pools. Giving both is an error.
func (r *reader) addressing(node *yaml.Node) Addressing {
	f, ok := r.mapping(node, "addressing", "$ref", "supernet", "pools")
	if !ok {
		return Addressing{}
	}
	var out Addressing
	out.Ref = f.text("$ref", false)
	if out.Ref != "" && (f.has("supernet") || f.has("pools")) {
		r.fail(node.Line, "addressing: give either $ref or an inline plan, not both")
	}
	if text := f.text("supernet", false); text != "" {
		out.Supernet = r.prefix(f.by["supernet"], "addressing.supernet", text)
	}
	for i, item := range f.sequence("pools") {
		out.Pools = append(out.Pools, r.pool(item, fmt.Sprintf("addressing.pools[%d]", i)))
	}
	return out
}

// pool reads one entry of an inline plan's pools: name and prefix are
// required; child_bits, reserved and skip_first are optional and range-checked.
// path is the entry's place in the file, used in messages.
func (r *reader) pool(node *yaml.Node, path string) AddressPool {
	f, ok := r.mapping(node, path, "name", "prefix", "child_bits", "reserved", "skip_first")
	if !ok {
		return AddressPool{}
	}
	pool := AddressPool{Name: f.text("name", true), Reserved: f.boolean("reserved")}
	if text := f.text("prefix", true); text != "" {
		pool.Prefix = r.prefix(f.by["prefix"], path+".prefix", text)
	}
	if bits, given := f.integer("child_bits"); given {
		if bits < 0 || bits > 32 {
			r.fail(f.by["child_bits"].Line, "%s.child_bits: %d is not between 0 and 32", path, bits)
		}
		pool.ChildBits = bits
	}
	if skip, given := f.integer("skip_first"); given {
		if skip < 0 {
			r.fail(f.by["skip_first"].Line, "%s.skip_first: %d is negative", path, skip)
		}
		pool.SkipFirst = skip
	}
	return pool
}

// site reads one entry of "sites": a required id, and optional provider and
// region. path is the entry's place in the file, used in messages.
func (r *reader) site(node *yaml.Node, path string) Site {
	f, ok := r.mapping(node, path, "id", "provider", "region")
	if !ok {
		return Site{}
	}
	return Site{ID: types.SiteID(f.text("id", true)), Provider: f.text("provider", false), Region: f.text("region", false)}
}

// node reads one entry of "nodes": required id, site and role, and optional nat,
// endpoints, advertises, labels and peers. Each value is checked as it is read
// (a bad role, endpoint or prefix is a located problem); whether the site
// exists is rule T3, checked later.
func (r *reader) node(node *yaml.Node, path string) Node {
	f, ok := r.mapping(node, path, "id", "site", "role", "endpoints", "nat", "advertises", "labels", "peers")
	if !ok {
		return Node{}
	}
	out := Node{ID: types.NodeID(f.text("id", true)), Site: types.SiteID(f.text("site", true))}

	if text := f.text("role", true); text != "" {
		role, valid := types.ParseRole(text)
		if !valid {
			r.fail(f.by["role"].Line, "%s.role: %q is not a role; use hub or spoke", path, text)
		}
		out.Role = role
	}
	if text := f.text("nat", false); text != "" {
		mode, valid := ParseNATMode(text)
		if !valid {
			r.fail(f.by["nat"].Line, "%s.nat: %q is not a NAT mode; use none, behind or unknown", path, text)
		}
		out.NAT = mode
	}
	for i, item := range f.sequence("endpoints") {
		itemPath := fmt.Sprintf("%s.endpoints[%d]", path, i)
		text, ok := r.scalarText(item, itemPath)
		if !ok {
			continue
		}
		endpoint, err := netip.ParseAddrPort(text)
		if err != nil {
			r.fail(item.Line, "%s: %q is not an address and port like 203.0.113.10:51820", itemPath, text)
			continue
		}
		out.Endpoints = append(out.Endpoints, endpoint)
	}
	for i, item := range f.sequence("advertises") {
		itemPath := fmt.Sprintf("%s.advertises[%d]", path, i)
		text, ok := r.scalarText(item, itemPath)
		if !ok {
			continue
		}
		if text == "site" {
			out.Advertises = append(out.Advertises, Advertisement{Site: true})
			continue
		}
		if prefix := r.prefix(item, itemPath, text); prefix.IsValid() {
			out.Advertises = append(out.Advertises, Advertisement{Prefix: prefix})
		}
	}
	if labels, given := f.by["labels"]; given {
		out.Labels = r.labels(labels, path+".labels")
	}
	for i, item := range f.sequence("peers") {
		itemPath := fmt.Sprintf("%s.peers[%d]", path, i)
		if text, ok := r.scalarText(item, itemPath); ok {
			out.Peers = append(out.Peers, types.NodeID(text))
		}
	}
	return out
}

// labels reads a mapping of text to text.
func (r *reader) labels(node *yaml.Node, path string) map[string]string {
	target := resolve(node)
	if target.Kind == yaml.ScalarNode && target.ShortTag() == "!!null" {
		return nil
	}
	if target.Kind != yaml.MappingNode {
		r.fail(node.Line, "%s: expected a mapping of label names to values", path)
		return nil
	}
	out := map[string]string{}
	for i := 0; i+1 < len(target.Content); i += 2 {
		key, value := target.Content[i], target.Content[i+1]
		text, ok := r.scalarText(value, path+"."+key.Value)
		if key.Kind != yaml.ScalarNode || key.Value == "" {
			r.fail(key.Line, "%s: a label name must be non-empty text", path)
			continue
		}
		if _, dup := out[key.Value]; dup {
			r.fail(key.Line, "%s: label %q is given twice", path, key.Value)
			continue
		}
		if ok {
			out[key.Value] = text
		}
	}
	return out
}

// link reads one entry of "links": required kind, a and b, and optional cost
// (positive) and health. It does not derive the ID or order the ends; Normalize
// does, and whether the nodes exist is rule T4, checked later.
func (r *reader) link(node *yaml.Node, path string) Link {
	f, ok := r.mapping(node, path, "kind", "a", "b", "cost", "health")
	if !ok {
		return Link{}
	}
	out := Link{A: types.NodeID(f.text("a", true)), B: types.NodeID(f.text("b", true))}
	if text := f.text("kind", true); text != "" {
		kind, valid := types.ParseLinkKind(text)
		if !valid {
			r.fail(f.by["kind"].Line, "%s.kind: %q is not a link kind; use hub-spoke or direct", path, text)
		}
		out.Kind = kind
	}
	if cost, given := f.integer("cost"); given {
		if cost < 1 {
			r.fail(f.by["cost"].Line, "%s.cost: %d is not positive; leave cost out for the default", path, cost)
		}
		out.Cost = cost
	}
	if text := f.text("health", false); text != "" {
		mode, valid := ParseHealthMode(text)
		if !valid {
			r.fail(f.by["health"].Line, "%s.health: %q is not a health mode; use required or assumed", path, text)
		}
		out.Health = mode
	}
	return out
}

// intent reads one entry of "intents": exactly one of allow or deny, whose body
// has from, to and optional ports (1 to 65535). The two selectors are parsed
// here; whether they match anything is rule T10, checked later.
func (r *reader) intent(node *yaml.Node, path string) Intent {
	item, ok := r.mapping(node, path, "allow", "deny")
	if !ok {
		return Intent{}
	}
	var out Intent
	var body *yaml.Node
	switch {
	case item.has("allow") && item.has("deny"):
		r.fail(node.Line, "%s: an intent is either allow or deny, not both", path)
		return Intent{}
	case item.has("allow"):
		out.Action, body, path = ActionAllow, item.by["allow"], path+".allow"
	case item.has("deny"):
		out.Action, body, path = ActionDeny, item.by["deny"], path+".deny"
	default:
		r.fail(node.Line, "%s: an intent needs allow or deny", path)
		return Intent{}
	}
	f, ok := r.mapping(body, path, "from", "to", "ports")
	if !ok {
		return out
	}
	out.From = r.selector(f, path, "from")
	out.To = r.selector(f, path, "to")
	for i, entry := range f.sequence("ports") {
		text, ok := r.scalarText(entry, fmt.Sprintf("%s.ports[%d]", path, i))
		if !ok {
			continue
		}
		port, err := strconv.Atoi(text)
		if err != nil || strconv.Itoa(port) != text || port < 1 || port > 65535 {
			r.fail(entry.Line, "%s.ports[%d]: %q is not a port between 1 and 65535", path, i, text)
			continue
		}
		out.Ports = append(out.Ports, uint16(port))
	}
	return out
}

// selector reads the required selector text at key ("from" or "to") of an
// intent body and parses it with ParseSelector. A bad selector is reported at
// the line of its value and the zero Selector is returned.
func (r *reader) selector(f fields, path, key string) Selector {
	text := f.text(key, true)
	if text == "" {
		return Selector{}
	}
	selector, err := ParseSelector(text)
	if err != nil {
		var coded *types.Error
		message := err.Error()
		if errors.As(err, &coded) {
			message = coded.Msg
		}
		r.fail(f.by[key].Line, "%s.%s: %s", path, key, message)
	}
	return selector
}
