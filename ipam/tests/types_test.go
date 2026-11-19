package tests

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/ipam"
)

// pfx parses a prefix, panicking on bad text; for use in test tables.
func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// P1-IPAM-050: an owner is one string in JSON and in text, and round-trips.
func TestOwnerRoundTrip(t *testing.T) {
	cases := map[ipam.Owner]string{
		ipam.Site("aws"):                 "site:aws",
		ipam.Node("builder-1"):           "node:builder-1",
		ipam.Link("a~b"):                 "link:a~b",
		ipam.Infra("dns"):                "infra:dns",
		{Kind: ipam.KindNode, ID: "x:y"}: "node:x:y", // the first ':' splits
	}
	for owner, text := range cases {
		if owner.String() != text {
			t.Errorf("String() = %q, want %q", owner.String(), text)
		}
		raw, err := json.Marshal(owner)
		if err != nil || string(raw) != `"`+text+`"` {
			t.Errorf("Marshal(%v) = %s, %v", owner, raw, err)
		}
		var back ipam.Owner
		if err := json.Unmarshal(raw, &back); err != nil || back != owner {
			t.Errorf("Unmarshal(%s) = %v, %v; want %v", raw, back, err, owner)
		}
	}
}

// P1-IPAM-051: bad owners are refused both ways and never half-written.
func TestOwnerRejectsBad(t *testing.T) {
	for _, text := range []string{"", "aws", "site:", ":aws", "host:aws", "Site:aws"} {
		sentinel := ipam.Site("keep")
		got := sentinel
		if err := got.UnmarshalText([]byte(text)); err == nil {
			t.Errorf("UnmarshalText(%q) succeeded with %v", text, got)
		}
		if got != sentinel {
			t.Errorf("UnmarshalText(%q) changed the receiver to %v", text, got)
		}
	}
	for _, owner := range []ipam.Owner{{}, {Kind: ipam.KindSite}, {Kind: "host", ID: "x"}} {
		if raw, err := owner.MarshalText(); err == nil {
			t.Errorf("MarshalText(%#v) = %q, want error", owner, raw)
		}
	}
}

// TestStateValid checks which State values are valid.
func TestStateValid(t *testing.T) {
	for _, state := range []ipam.State{ipam.Allocated, ipam.Reserved, ipam.Quarantined} {
		if !state.Valid() {
			t.Errorf("%q should be valid", state)
		}
	}
	for _, state := range []ipam.State{"", "free", "Allocated"} {
		if state.Valid() {
			t.Errorf("%q should be invalid", state)
		}
	}
}

// sampleSnapshot returns a snapshot with one allocation of each state, deliberately not in sorted
// order.
func sampleSnapshot() ipam.Snapshot {
	created := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	return ipam.Snapshot{
		Schema:   ipam.Schema,
		Supernet: pfx("10.42.0.0/16"),
		Pools: []ipam.Pool{
			{Name: "sites", Prefix: pfx("10.42.0.0/17"), ChildBits: 20},
			{Name: "overlay", Prefix: pfx("10.42.240.0/21"), ChildBits: 32, SkipFirst: 1},
			{Name: "growth", Prefix: pfx("10.42.128.0/18"), ChildBits: 20, Reserved: true},
		},
		Allocations: []ipam.Allocation{
			{Prefix: pfx("10.42.240.1/32"), Pool: "overlay", Owner: ipam.Node("builder-1"), State: ipam.Allocated, CreatedAt: created},
			{Prefix: pfx("10.42.0.0/20"), Pool: "sites", Owner: ipam.Site("aws"), State: ipam.Allocated, CreatedAt: created},
			{
				Prefix: pfx("10.42.16.0/20"), Pool: "sites", Owner: ipam.Site("old"), State: ipam.Quarantined,
				CreatedAt: created, QuarantineUntil: created.Add(24 * time.Hour), Note: "released",
			},
		},
	}
}

// P1-IPAM-052: sorting orders by cidr.Compare and is what makes bytes repeatable.
func TestSortAllocations(t *testing.T) {
	snap := sampleSnapshot()
	snap.SortAllocations()
	want := []string{"10.42.0.0/20", "10.42.16.0/20", "10.42.240.1/32"}
	for i, alloc := range snap.Allocations {
		if alloc.Prefix.String() != want[i] {
			t.Fatalf("position %d = %s, want %s", i, alloc.Prefix, want[i])
		}
	}
}

// P1-IPAM-053: a snapshot survives JSON unchanged and reads like a human file.
func TestSnapshotJSONRoundTrip(t *testing.T) {
	snap := sampleSnapshot()
	snap.SortAllocations()
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		`"schema": "beaver.ipam/v1"`, `"supernet": "10.42.0.0/16"`, `"owner": "site:aws"`,
		`"state": "quarantined"`, `"prefix": "10.42.240.1/32"`, `"skip_first": 1`, `"reserved": true`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("JSON is missing %s\n%s", want, text)
		}
	}
	if n := strings.Count(text, "quarantine_until"); n != 1 {
		t.Errorf("quarantine_until appears %d times, want 1 (only the quarantined block)", n)
	}
	var back ipam.Snapshot
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	again, err := json.MarshalIndent(back, "", "  ")
	if err != nil || string(again) != text {
		t.Fatalf("round trip changed the bytes (err %v)\nfirst:\n%s\nsecond:\n%s", err, text, again)
	}
	for i := range snap.Allocations {
		if !snap.Allocations[i].CreatedAt.Equal(back.Allocations[i].CreatedAt) ||
			snap.Allocations[i].Owner != back.Allocations[i].Owner {
			t.Errorf("allocation %d changed: %+v -> %+v", i, snap.Allocations[i], back.Allocations[i])
		}
	}
}

// TestSnapshotRejectsBadOwnerInJSON checks that JSON naming an owner of an unknown kind fails to
// load.
func TestSnapshotRejectsBadOwnerInJSON(t *testing.T) {
	var snap ipam.Snapshot
	err := json.Unmarshal([]byte(`{"allocations":[{"owner":"nobody"}]}`), &snap)
	if err == nil {
		t.Fatal("want an error for owner \"nobody\"")
	}
}

// TestReportJSONFieldNames pins the JSON field names of an accounting Report, since callers depend
// on them.
func TestReportJSONFieldNames(t *testing.T) {
	raw, err := json.Marshal(ipam.Report{Supernet: pfx("10.42.0.0/16"), Total: 65536, Free: 65536,
		FreeBlocks: []netip.Prefix{pfx("10.42.0.0/16")}, Pools: []ipam.PoolUsage{}, Problems: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"supernet":"10.42.0.0/16","total":65536,"allocated":0,"reserved":0,"quarantined":0,"free":65536,"free_blocks":["10.42.0.0/16"],"pools":[],"problems":[]}`
	if string(raw) != want {
		t.Errorf("got  %s\nwant %s", raw, want)
	}
}
