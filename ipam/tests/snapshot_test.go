package tests

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/types"
)

// populated builds a registry holding a mix of allocated, reserved and quarantined blocks, for the
// save/load tests.
func populated(t *testing.T) *ipam.IPAM {
	t.Helper()
	registry, clock := withClock(t, time.Hour)
	for _, name := range []string{"aws", "builder", "dgx"} {
		mustAllocate(t, registry, "sites", ipam.Site(name))
		clock.Advance(time.Minute)
	}
	for _, name := range []string{"aws-hub", "builder-1", "dgx-spark"} {
		mustAllocate(t, registry, "overlay", ipam.Node(name), ipam.WithNote("machine "+name))
	}
	if _, err := registry.AllocateTransit("builder-1~dgx-spark"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Reserve(pfx("10.42.48.0/20"), ipam.Site("next"), "planned"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Release(pfx("10.42.240.3/32"), ipam.Node("dgx-spark")); err != nil {
		t.Fatal(err)
	}
	return registry
}

// P1-IPAM-040: save, load, save gives identical bytes, and the loaded state is
// accepted by New and accounts the same.
func TestSnapshotSaveLoadSave(t *testing.T) {
	registry := populated(t)
	first, err := ipam.Marshal(registry.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ipam.Unmarshal(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ipam.Marshal(loaded)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("bytes changed (err %v)\n--- first\n%s\n--- second\n%s", err, first, second)
	}
	again, err := withClockNew(t, loaded)
	if err != nil {
		t.Fatalf("New refused its own snapshot: %v", err)
	}
	if a, b := registry.Accounting(), again.Accounting(); a.Allocated != b.Allocated || a.Free != b.Free ||
		a.Reserved != b.Reserved || a.Quarantined != b.Quarantined || len(a.FreeBlocks) != len(b.FreeBlocks) {
		t.Errorf("accounting changed: %+v vs %+v", a, b)
	}
	if !bytes.HasSuffix(first, []byte("}\n")) {
		t.Errorf("want a trailing newline, got %q", first[len(first)-3:])
	}
}

// withClockNew builds a registry from snap with a clock ten minutes after the test start, so
// expiry can be tested.
func withClockNew(t *testing.T, snap ipam.Snapshot) (*ipam.IPAM, error) {
	t.Helper()
	return ipam.New(snap, &types.FixedClock{T: t0.Add(10 * time.Minute)}, time.Hour)
}

// Done when: equal states serialise to equal bytes, however they were built.
func TestMarshalIsOrderAndZoneIndependent(t *testing.T) {
	zone := time.FixedZone("plus3", 3*3600)
	one := sampleSnapshot()
	two := sampleSnapshot()
	two.Allocations[0], two.Allocations[2] = two.Allocations[2], two.Allocations[0]
	for i := range two.Allocations {
		two.Allocations[i].CreatedAt = two.Allocations[i].CreatedAt.In(zone)
		two.Allocations[i].QuarantineUntil = two.Allocations[i].QuarantineUntil.In(zone)
	}
	a, errA := ipam.Marshal(one)
	b, errB := ipam.Marshal(two)
	if errA != nil || errB != nil || !bytes.Equal(a, b) {
		t.Fatalf("different bytes (%v, %v)\n%s\n%s", errA, errB, a, b)
	}
	if strings.Contains(string(a), "+03:00") {
		t.Error("times must be written in UTC")
	}
	if one.Allocations[0].Prefix != pfx("10.42.240.1/32") {
		t.Error("Marshal sorted the caller's snapshot")
	}
}

// TestMarshalWritesEmptyListsAndNoDerivedData checks that empty lists are written as [] and that
// no computed figures appear in the file.
func TestMarshalWritesEmptyListsAndNoDerivedData(t *testing.T) {
	raw, err := ipam.Marshal(ipam.Snapshot{Schema: ipam.Schema, Supernet: pfx("10.42.0.0/16")})
	if err != nil {
		t.Fatal(err)
	}
	const want = "{\n  \"schema\": \"beaver.ipam/v1\",\n  \"supernet\": \"10.42.0.0/16\",\n  \"pools\": [],\n  \"allocations\": []\n}\n"
	if string(raw) != want {
		t.Errorf("got\n%q\nwant\n%q", raw, want)
	}
	registry := populated(t)
	registry.Warnings()
	out, _ := ipam.Marshal(registry.Snapshot())
	for _, derived := range []string{"warning", "free_blocks", "problems", "total"} {
		if strings.Contains(string(out), derived) {
			t.Errorf("snapshot contains derived data %q", derived)
		}
	}
}

// TestMarshalRefusesBadInput checks that Marshal refuses an unknown schema and other invalid
// snapshots.
func TestMarshalRefusesBadInput(t *testing.T) {
	bad := sampleSnapshot()
	bad.Schema = "beaver.ipam/v0"
	if _, err := ipam.Marshal(bad); !types.HasCode(err, types.ErrUnsupportedSchema) {
		t.Errorf("schema: %v", err)
	}
	bad = sampleSnapshot()
	bad.Allocations[0].Owner = ipam.Owner{}
	if _, err := ipam.Marshal(bad); err == nil {
		t.Error("zero owner written")
	}
}

// TestSnapshotMethodIsACopyAndDropsExpired checks that Snapshot returns a copy and omits
// quarantines that have already ended.
func TestSnapshotMethodIsACopyAndDropsExpired(t *testing.T) {
	registry, clock := withClock(t, time.Hour)
	got := mustAllocate(t, registry, "overlay", ipam.Node("a"))
	snap := registry.Snapshot()
	snap.Allocations[0].Owner = ipam.Node("tampered")
	snap.Pools[0].Name = "tampered"
	if allocsOf(registry)[0].Owner != ipam.Node("a") || registry.Snapshot().Pools[0].Name != "sites" {
		t.Error("Snapshot shares memory with the registry")
	}
	if err := registry.Release(got.Prefix, ipam.Node("a")); err != nil {
		t.Fatal(err)
	}
	if n := len(registry.Snapshot().Allocations); n != 1 {
		t.Errorf("%d allocations while quarantined, want 1", n)
	}
	clock.Advance(time.Hour)
	if n := len(registry.Snapshot().Allocations); n != 0 {
		t.Errorf("%d allocations after quarantine, want 0", n)
	}
}

// P1-IPAM-041 at the file level.
func TestUnmarshalRefusesUnknownSchema(t *testing.T) {
	for _, text := range []string{
		`{"schema":"beaver.ipam/v9","supernet":"10.42.0.0/16","pools":[],"allocations":[]}`,
		`{"supernet":"10.42.0.0/16"}`,
		`{}`,
		`null`,
	} {
		if _, err := ipam.Unmarshal([]byte(text)); !types.HasCode(err, types.ErrUnsupportedSchema) {
			t.Errorf("%s: want UNSUPPORTED_SCHEMA, got %v", text, err)
		}
	}
}

// TestUnmarshalIsStrict checks that unknown fields, trailing data, bad owners and similar are all
// refused.
func TestUnmarshalIsStrict(t *testing.T) {
	const head = `{"schema":"beaver.ipam/v1","supernet":"10.42.0.0/16","pools":[],`
	bad := map[string]string{
		"empty":            ``,
		"not json":         `hello`,
		"array":            `[]`,
		"unknown field":    head + `"allocations":[],"extra":1}`,
		"unknown nested":   head + `"allocations":[{"prefix":"10.42.0.0/20","pool":"sites","owner":"site:aws","state":"allocated","created_at":"2026-11-19T09:00:00Z","color":"red"}]}`,
		"bad owner":        head + `"allocations":[{"prefix":"10.42.0.0/20","pool":"sites","owner":"nobody","state":"allocated","created_at":"2026-11-19T09:00:00Z"}]}`,
		"bad prefix":       head + `"allocations":[{"prefix":"10.42.0.0/99","pool":"sites","owner":"site:aws","state":"allocated","created_at":"2026-11-19T09:00:00Z"}]}`,
		"bad time":         head + `"allocations":[{"prefix":"10.42.0.0/20","pool":"sites","owner":"site:aws","state":"allocated","created_at":"yesterday"}]}`,
		"wrong type":       head + `"allocations":"none"}`,
		"trailing data":    head + `"allocations":[]}{}`,
		"trailing garbage": head + `"allocations":[]} x`,
		"truncated":        head + `"allocations":[`,
	}
	for name, text := range bad {
		if _, err := ipam.Unmarshal([]byte(text)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	good := head + `"allocations":[]}` + "\n  \n"
	if _, err := ipam.Unmarshal([]byte(good)); err != nil {
		t.Errorf("trailing whitespace refused: %v", err)
	}
}

// TestUnmarshalSortsAllocations checks that allocations come back in sorted order whatever order
// the file had.
func TestUnmarshalSortsAllocations(t *testing.T) {
	snap := sampleSnapshot() // deliberately unsorted
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ipam.Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(loaded.Allocations); i++ {
		if loaded.Allocations[i-1].Prefix.Addr().Compare(loaded.Allocations[i].Prefix.Addr()) > 0 {
			t.Fatalf("not sorted: %v", loaded.Allocations)
		}
	}
}

// A hand-edited file loads, then New names every problem (P1-E2E-061 path).
func TestCorruptFileIsCaughtByNew(t *testing.T) {
	good, err := ipam.Marshal(populated(t).Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(good), `"prefix": "10.42.16.0/20"`, `"prefix": "10.42.0.0/21"`, 1)
	loaded, err := ipam.Unmarshal([]byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withClockNew(t, loaded); !types.HasCode(err, types.ErrOverlap) {
		t.Errorf("want OVERLAP, got %v", err)
	}
}

// P1-IPAM-042: FuzzSnapshot never panics; whatever loads can be saved again to
// the same bytes.
func FuzzSnapshot(f *testing.F) {
	good, _ := ipam.Marshal(sampleSnapshot())
	f.Add(good)
	f.Add([]byte(`{"schema":"beaver.ipam/v1"}`))
	f.Add([]byte(`{"schema":"beaver.ipam/v1","supernet":"10.42.0.0/16","pools":[{"name":"p","prefix":"10.42.0.0/17","child_bits":20}],"allocations":[{"prefix":"10.42.0.0/20","pool":"p","owner":"site:a","state":"allocated","created_at":"2026-11-19T09:00:00Z"}]}`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, data []byte) {
		snap, err := ipam.Unmarshal(data)
		if err != nil {
			return
		}
		first, err := ipam.Marshal(snap)
		if err != nil {
			return // loadable but not writable, e.g. a time year outside JSON's range
		}
		again, err := ipam.Unmarshal(first)
		if err != nil {
			t.Fatalf("Marshal output does not load: %v\n%s", err, first)
		}
		second, err := ipam.Marshal(again)
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("save/load/save changed bytes (%v)\n%s\n%s", err, first, second)
		}
		// New may refuse the snapshot, but it must never panic.
		_, _ = ipam.New(snap, &types.FixedClock{T: t0}, time.Hour)
	})
}
