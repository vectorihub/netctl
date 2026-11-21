// Package config turns a plan into one [NodeConfig] per node: the desired network
// state of that node, written in Beaver's own words and independent of the machine
// that will apply it.
//
// A NodeConfig is the line between the control plane and the data plane. It says
// what: the node's overlay address, its peers, the routes, what to probe, who
// forwards. It never says how: no interface names, no ip, wg or nft syntax, no
// sysctls, no routing-table numbers, no file paths. The same document can be
// applied by a Linux agent today and by another kind of agent later. It holds no
// secrets: a peer's public key is named by reference, and private keys are made on
// the node and never leave it ([ScanSecrets] checks).
//
// # Generating
//
// [Generate] builds every node's config from one plan in one pass. The rule that
// matters most is the one for each peer's allowed prefixes: WireGuard keeps a
// prefix with a single peer and sends its traffic there, so a peer lists only the
// prefixes whose active route goes through it, plus its probe address. When a
// direct link fails, the far node's prefixes leave the direct peer's list and join
// the hub's, together, in the next config, and the agent applies it as one change.
// See [AllowedPrefixes].
//
// # Same input, same bytes
//
// [NodeConfig.Encode] sorts every list, writes the keys in a fixed order and puts
// the SHA-256 of the result in the hash field. Two plans can be told apart by hash,
// and a config is only pushed when its hash changes. [PlanID] names a whole plan
// by a hash of every node's content. [Diff] says what changed between two configs
// of a node: peers added or removed, prefixes moved from one peer to another, and
// routes changed.
//
// # The schema
//
// config/schema/v1alpha1.json is the JSON Schema of a config, for consumers that
// are not written in Go; [SchemaJSON] returns the same bytes. v1alpha1 may change
// between releases until it is declared stable.
package config
