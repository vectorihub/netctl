package e2e

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// update rewrites golden files to whatever the code produces now. Run it only after reading
// the diff a failing test printed, and commit the change on its own with the reason.
var update = flag.Bool("update", false, "rewrite golden files")

// GoldenRoot is the directory of the reviewed expected outputs, under testdata/.
const GoldenRoot = "golden/e2e"

// maskPlanID and maskHash blank the two fields that change whenever anything in a config
// changes, so a golden file can compare the rest of a config.
var (
	maskPlanID = regexp.MustCompile(`"plan_id": "[^"]*"`)
	maskHash   = regexp.MustCompile(`"hash": "[^"]*"`)
)

// Masked hides the plan_id and hash of a rendered config so the rest can be compared.
//
// Example:
//
//	w.Golden("builder-1-healthy.json", e2e.Masked(out.Files["nodes/builder-1.json"]))
func Masked(data []byte) []byte {
	data = maskPlanID.ReplaceAll(data, []byte(`"plan_id": "<masked>"`))
	return maskHash.ReplaceAll(data, []byte(`"hash": "<masked>"`))
}

// Golden compares got with testdata/golden/e2e/<test-id>/<name>. With -update it writes got
// instead. A difference fails the test and names the first differing line.
//
// Example:
//
//	w.Golden("routes-fresh.txt", out.Files["routes.txt"])
func (w *W) Golden(name string, got []byte) {
	w.T.Helper()
	root, err := ModuleRoot()
	if err != nil {
		w.Fatalf("golden: %v", err)
	}
	path := filepath.Join(root, "testdata", filepath.FromSlash(GoldenRoot), w.ID, filepath.FromSlash(name))
	GoldenAt(w, path, got)
}

// GoldenAt is Golden for an explicit file path, shared with the lab tests whose goldens
// live beside their observations.
//
// Example:
//
//	e2e.GoldenAt(w, path, data)
func GoldenAt(w *W, path string, got []byte) {
	w.T.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			w.Fatalf("golden %s: %v", path, err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			w.Fatalf("golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		w.Fatalf("golden %s: %v (run with -update after reviewing the output)", path, err)
	}
	if string(want) != string(got) {
		w.Fatalf("golden %s differs: %s", filepath.Base(path), firstLineDiff(string(want), string(got)))
	}
}

// GoldenDir compares every file of a plan's output with the golden directory of that name
// (testdata/golden/e2e/<test-id>/<name>/), in both directions: a file that exists on only
// one side fails.
//
// Example:
//
//	w.GoldenDir(out, "baseline")
func (w *W) GoldenDir(o *Out, name string) {
	w.T.Helper()
	for path, data := range o.Files {
		w.Golden(filepath.Join(name, filepath.FromSlash(path)), Masked(data))
	}
	root, err := ModuleRoot()
	if err != nil {
		w.Fatalf("golden: %v", err)
	}
	base := filepath.Join(root, "testdata", filepath.FromSlash(GoldenRoot), w.ID, name)
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		if _, ok := o.Files[filepath.ToSlash(rel)]; ok {
			return nil
		}
		if !*update {
			w.Fatalf("golden %s has %s, the plan does not", name, rel)
			return nil
		}
		// -update makes the directory match the plan: a file the plan no longer
		// produces is removed, so the next normal run agrees.
		if rmErr := os.Remove(path); rmErr != nil {
			w.Fatalf("golden %s: removing stale %s: %v", name, rel, rmErr)
		}
		return nil
	})
}

// firstLineDiff describes where two texts first differ, for a failure message.
func firstLineDiff(want, got string) string {
	a, b := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return fmt.Sprintf("line %d\n   want: %s\n   got:  %s", i+1, x, y)
		}
	}
	return "identical"
}
