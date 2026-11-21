package tests

import (
	"os"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/internal/covercheck"
)

// TestCoverageMeetsTheGate reads the profile named by COVER_PROFILE (make cover writes it)
// and fails for any package below its threshold. Without the variable it skips.
func TestCoverageMeetsTheGate(t *testing.T) {
	path := os.Getenv("COVER_PROFILE")
	if path == "" {
		t.Skip("COVER_PROFILE is not set; run make cover")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	results, err := covercheck.Check(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		t.Log(r)
		if !r.Ok() {
			t.Errorf("%s", r)
		}
	}
}

// TestCheckCountsABlockOnceAndAnyHitCovers checks the merge of a profile written by several
// test binaries: the same block twice is one block, covered if either run hit it.
func TestCheckCountsABlockOnceAndAnyHitCovers(t *testing.T) {
	profile := strings.Join([]string{
		"mode: set",
		"github.com/vectorihub/netctl/ipam/a.go:1.1,2.2 4 0",
		"github.com/vectorihub/netctl/ipam/a.go:1.1,2.2 4 1",
		"github.com/vectorihub/netctl/ipam/a.go:3.1,4.2 6 0",
	}, "\n")
	results, err := covercheck.Check(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Package == "ipam" && (r.Percent < 39.9 || r.Percent > 40.1) {
			t.Errorf("ipam coverage %.1f, want 40", r.Percent)
		}
	}
}
