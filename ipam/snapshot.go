package ipam

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/vectorihub/netctl/types"
)

// Snapshot returns the complete state as a value, ready for Marshal. It is a
// deep copy: changing it does not touch the registry. Quarantined blocks whose
// time has passed are dropped first, so a snapshot never carries expired state.
// Allocations are sorted by cidr.Compare.
func (registry *IPAM) Snapshot() Snapshot {
	registry.expire()
	return Snapshot{
		Schema:      Schema,
		Supernet:    registry.supernet,
		Pools:       slices.Clone(registry.pools),
		Allocations: slices.Clone(registry.allocs),
	}
}

// Marshal writes snapshot as indented JSON with a trailing newline. It does no
// file access: the caller stores the bytes.
//
// The output is byte-stable: allocations are sorted, times are written in UTC,
// empty lists are written as [] and nothing derived is included, so equal
// states always give equal bytes. snapshot itself is not changed. It fails if
// the schema is not Schema or an owner is not valid. It does not check the
// rules New checks; New does that when the snapshot is loaded back.
func Marshal(snapshot Snapshot) ([]byte, error) {
	if snapshot.Schema != Schema {
		return nil, types.Errf(types.ErrUnsupportedSchema, "schema", "got %q, want %q", snapshot.Schema, Schema)
	}
	tidy := snapshot
	tidy.Pools = append([]Pool{}, snapshot.Pools...)
	tidy.Allocations = append([]Allocation{}, snapshot.Allocations...)
	for i := range tidy.Allocations {
		tidy.Allocations[i].CreatedAt = tidy.Allocations[i].CreatedAt.UTC()
		tidy.Allocations[i].QuarantineUntil = tidy.Allocations[i].QuarantineUntil.UTC()
	}
	tidy.SortAllocations()

	raw, err := json.MarshalIndent(tidy, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("ipam: marshal snapshot: %w", err)
	}
	return append(raw, '\n'), nil
}

// Unmarshal reads a snapshot written by Marshal. It is strict, so a corrupted
// or hand-edited file is refused instead of half-read:
//   - a schema other than Schema (missing included) is UNSUPPORTED_SCHEMA, checked
//     before anything else;
//   - unknown fields, wrong types, bad owners, bad prefixes and trailing data
//     after the JSON value are errors.
//
// The allocations come back sorted. Unmarshal does not apply the rules New
// applies (overlaps, pools, supernet); pass the result to New for that.
func Unmarshal(data []byte) (Snapshot, error) {
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return Snapshot{}, fmt.Errorf("ipam: read snapshot: %w", err)
	}
	if header.Schema != Schema {
		return Snapshot{}, types.Errf(types.ErrUnsupportedSchema, "schema", "got %q, want %q", header.Schema, Schema)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("ipam: read snapshot: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Snapshot{}, errors.New("ipam: read snapshot: unexpected data after the JSON value")
	}
	snapshot.SortAllocations()
	return snapshot, nil
}
