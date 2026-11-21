package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vectorihub/netctl/ipam"
	"github.com/vectorihub/netctl/sim"
	"github.com/vectorihub/netctl/types"
)

// ExpectOK fails the test unless the plan succeeded.
//
// Example:
//
//	w.ExpectOK(out) // fails the test unless the plan succeeded and wrote its files
func (w *W) ExpectOK(o *Out) {
	w.T.Helper()
	if !o.OK() {
		w.Fatalf("plan %s: want success, got %v", o.Name, o.Err)
	}
}

// ExpectRefused fails the test unless the plan was refused with the given error code, and
// unless the refusal wrote no output directory. It is the shared check of the bad-input
// tests: refuse, say why, change nothing.
//
// Example:
//
//	w.ExpectRefused(out, types.ErrOverlap)
func (w *W) ExpectRefused(o *Out, code types.Code) {
	w.T.Helper()
	if o.Err == nil {
		w.Fatalf("plan %s: want error %s, got success", o.Name, code)
	}
	if !types.HasCode(o.Err, code) {
		w.Fatalf("plan %s: want error %s, got %v", o.Name, code, o.Err)
	}
	w.ExpectNoDir(o.Name)
}

// ExpectNoDir fails the test if a directory of that name exists in the workspace.
//
// Example:
//
//	w.ExpectNoDir("out-2") // a refused plan leaves no output directory
func (w *W) ExpectNoDir(name string) {
	w.T.Helper()
	if _, err := os.Stat(filepath.Join(w.Dir, name)); err == nil {
		w.Fatalf("%s exists; a refused plan must not create its output directory", name)
	}
}

// ExpectFiles fails the test unless every named file exists in the plan's output.
//
// Example:
//
//	w.ExpectFiles(out, "plan.json", "ipam.json")
func (w *W) ExpectFiles(o *Out, names ...string) {
	w.T.Helper()
	for _, name := range names {
		if _, ok := o.Files[name]; !ok {
			w.Fatalf("%s lacks %s", o.Name, name)
		}
	}
}

// Snapshot is the SHA-256 of every file under a directory, by relative path.
//
// Example:
//
//	snap := e2e.Snapshot{"plan.json": "9f2c..."} // path to content hash
type Snapshot map[string]string

// Snapshot hashes every file under dir (a workspace-relative or absolute path).
//
// Example:
//
//	before := w.Snapshot("out-1")
func (w *W) Snapshot(dir string) Snapshot {
	w.T.Helper()
	snap, err := SnapshotDir(w.abs(dir))
	if err != nil {
		w.Fatalf("snapshot %s: %v", dir, err)
	}
	return snap
}

// abs makes a workspace-relative path absolute.
func (w *W) abs(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(w.Dir, path)
}

// SnapshotDir hashes every file under dir. A missing directory gives an empty snapshot.
//
// Example:
//
//	snap, err := e2e.SnapshotDir(dir)
func SnapshotDir(dir string) (Snapshot, error) {
	snap := Snapshot{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		sum := sha256.Sum256(data)
		snap[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if os.IsNotExist(err) {
		return snap, nil
	}
	return snap, err
}

// ExpectUnchanged fails the test unless the directory is byte-identical to the snapshot,
// naming the first file that differs.
//
// Example:
//
//	w.ExpectUnchanged("out-1", before) // before := w.Snapshot("out-1")
func (w *W) ExpectUnchanged(dir string, before Snapshot) {
	w.T.Helper()
	after := w.Snapshot(dir)
	if name, differs := firstDifference(before, after); differs {
		w.Fatalf("%s changed: %s differs", dir, name)
	}
}

// firstDifference names the first path, in sorted order, where two snapshots disagree.
func firstDifference(a, b Snapshot) (string, bool) {
	names := map[string]bool{}
	for name := range a {
		names[name] = true
	}
	for name := range b {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	slices.Sort(sorted)
	for _, name := range sorted {
		if a[name] != b[name] {
			return name, true
		}
	}
	return "", false
}

// DirHash is one hash over a whole tree: every path and every file's hash, in path order. Two
// directories have equal hashes when they hold the same files with the same bytes.
//
// Example:
//
//	sum := w.DirHash("out-1")
func (w *W) DirHash(dir string) string {
	w.T.Helper()
	return HashSnapshot(w.Snapshot(dir))
}

// HashSnapshot reduces a snapshot to one hash.
//
// Example:
//
//	sum := e2e.HashSnapshot(w.Snapshot("out-1"))
func HashSnapshot(snap Snapshot) string {
	names := make([]string, 0, len(snap))
	for name := range snap {
		names = append(names, name)
	}
	slices.Sort(names)
	h := sha256.New()
	for _, name := range names {
		fmt.Fprintf(h, "%s %s\n", name, snap[name])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// FileHash is the SHA-256 of one rendered file of a plan.
//
// Example:
//
//	sum := out.FileHash("plan.json")
func (o *Out) FileHash(path string) string {
	sum := sha256.Sum256(o.Files[path])
	return hex.EncodeToString(sum[:])
}

// SubtreeHash is DirHash for the files of a plan under one prefix, for example "nodes/".
//
// Example:
//
//	sum := out.SubtreeHash("nodes/")
func (o *Out) SubtreeHash(prefix string) string {
	snap := Snapshot{}
	for path := range o.Files {
		if strings.HasPrefix(path, prefix) {
			snap[path] = o.FileHash(path)
		}
	}
	return HashSnapshot(snap)
}

// J is a parsed JSON document with a small query language: dot-separated keys, array
// indices (peers.1.allowed_prefixes.0) and [field=value] selectors that pick a list element
// by one of its fields (peers[node=aws-hub].allowed_prefixes).
//
// Example:
//
//	j := w.ParseJSON(out.Files["plan.json"]) // then j.Str("plan_id")
type J struct {
	w *W
	v any
}

// ParseJSON parses a rendered file of a plan for querying. It fails the test if the file is
// missing or not JSON.
//
// Example:
//
//	w.JSON(out, "nodes/builder-1.json").Strings("peers[node=aws-hub].allowed_prefixes")
func (w *W) JSON(o *Out, path string) *J {
	w.T.Helper()
	data, ok := o.Files[path]
	if !ok {
		w.Fatalf("%s lacks %s", o.Name, path)
	}
	return w.ParseJSON(data)
}

// ParseJSON parses JSON bytes into a queryable document.
//
// Example:
//
//	j := w.ParseJSON(out.Files["validation.json"])
func (w *W) ParseJSON(data []byte) *J {
	w.T.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		w.Fatalf("not JSON: %v", err)
	}
	return &J{w: w, v: v}
}

// Get returns the value at the path, or nil if there is none.
//
// Example:
//
//	v := j.Get("findings.0.rule") // nil when the path is absent
func (j *J) Get(path string) any {
	cur := j.v
	for _, part := range splitPath(path) {
		cur = step(cur, part)
		if cur == nil {
			return nil
		}
	}
	return cur
}

// splitPath cuts a query path into its parts. Dots separate keys, but a dot inside a
// [field=value] selector belongs to the value, so each selector is kept whole as a part of
// its own.
func splitPath(path string) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		}
	}
	for _, r := range path {
		switch {
		case r == '[' && depth == 0:
			flush()
			depth++
			cur.WriteRune(r)
		case r == ']' && depth == 1:
			cur.WriteRune(r)
			depth--
			flush()
		case r == '.' && depth == 0:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return parts
}

// step applies one query part to a value.
func step(v any, part string) any {
	if strings.HasPrefix(part, "[") {
		field, want, _ := strings.Cut(strings.Trim(part, "[]"), "=")
		list, _ := v.([]any)
		for _, item := range list {
			if m, ok := item.(map[string]any); ok && fmt.Sprint(m[field]) == want {
				return item
			}
		}
		return nil
	}
	switch t := v.(type) {
	case map[string]any:
		return t[part]
	case []any:
		if i, err := strconv.Atoi(part); err == nil && i >= 0 && i < len(t) {
			return t[i]
		}
	}
	return nil
}

// Str returns the string at the path, or "" if there is none. Numbers and booleans are
// written the way fmt prints them.
//
// Example:
//
//	rule := j.Str("findings[rule=V-ADDR-006].severity")
func (j *J) Str(path string) string {
	switch v := j.Get(path).(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// Num returns the number at the path, or 0 if there is none.
//
// Example:
//
//	n := j.Num("summary.errors")
func (j *J) Num(path string) float64 {
	n, _ := j.Get(path).(float64)
	return n
}

// Bool returns the boolean at the path, or false if there is none.
//
// Example:
//
//	pass := j.Bool("all_pass")
func (j *J) Bool(path string) bool {
	b, _ := j.Get(path).(bool)
	return b
}

// Strings returns the list of strings at the path; non-strings are skipped.
//
// Example:
//
//	rules := j.Strings("peers[node=aws-hub].allowed_prefixes")
func (j *J) Strings(path string) []string {
	list, _ := j.Get(path).([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Len returns the length of the list or object at the path, or -1 if there is neither.
//
// Example:
//
//	n := j.Len("findings")
func (j *J) Len(path string) int {
	switch v := j.Get(path).(type) {
	case []any:
		return len(v)
	case map[string]any:
		return len(v)
	}
	return -1
}

// ExpectFinding fails the test unless the plan's validation holds a finding of that rule,
// level ("error" or "warning") and subject.
//
// Example:
//
//	w.ExpectFinding(out, "V-TOPO-004", "warning", "builder-1~dgx-spark")
func (w *W) ExpectFinding(o *Out, rule, level, subject string) {
	w.T.Helper()
	for _, f := range o.Result.Validation.Findings {
		if f.Rule == rule && string(f.Level) == level && f.Subject == subject {
			return
		}
	}
	w.Fatalf("%s: no %s %s on %s; findings: %s", o.Name, level, rule, subject, findingList(o))
}

// findingList writes a plan's findings on one line each, for failure messages.
func findingList(o *Out) string {
	var b strings.Builder
	for _, f := range o.Result.Validation.Findings {
		fmt.Fprintf(&b, "\n   %s %s %s", f.Level, f.Rule, f.Subject)
	}
	return b.String()
}

// ExpectNoErrors fails the test if the plan's validation holds an error.
//
// Example:
//
//	w.ExpectNoErrors(out) // no validation finding of severity error
func (w *W) ExpectNoErrors(o *Out) {
	w.T.Helper()
	if errs := o.Result.Validation.Errors(); len(errs) > 0 {
		w.Fatalf("%s: validation errors: %s", o.Name, findingList(o))
	}
}

// ExpectSimPass fails the test unless the plan's simulation passed, and unless it ran n
// flows when n is not negative.
//
// Example:
//
//	w.ExpectSimPass(out, 6) // six flows simulated, all passing
func (w *W) ExpectSimPass(o *Out, flows int) {
	w.T.Helper()
	rep := o.Result.Simulation
	if !rep.Passed {
		w.Fatalf("%s: simulation failed: %+v", o.Name, rep.Summary)
	}
	if flows >= 0 && len(rep.Flows) != flows {
		w.Fatalf("%s: simulation ran %d flows, want %d", o.Name, len(rep.Flows), flows)
	}
}

// Flow returns the simulation result whose label is exactly the one given, for example
// "builder-1→dgx-spark (overlay)". It fails the test if there is none.
//
// Example:
//
//	flow := w.Flow(out, "builder-to-dgx")
func (w *W) Flow(o *Out, label string) sim.FlowResult {
	w.T.Helper()
	for _, f := range o.Result.Simulation.Flows {
		if f.Flow.Label == label {
			return f
		}
	}
	w.Fatalf("%s: no flow %q", o.Name, label)
	return sim.FlowResult{}
}

// Accounting is the plan's address accounting, what `ipam show` prints: it loads the saved
// address plan back through the ipam package, so a plan that could not be loaded fails here.
//
// Example:
//
//	report := w.Accounting(out) // allocated + reserved + quarantined + free == total
func (w *W) Accounting(o *Out) ipam.Report {
	w.T.Helper()
	snap, err := ipam.Unmarshal(o.Files["ipam.json"])
	if err != nil {
		w.Fatalf("%s: ipam.json: %v", o.Name, err)
	}
	registry, err := ipam.New(snap, &types.FixedClock{T: w.Now}, ipam.DefaultQuarantine)
	if err != nil {
		w.Fatalf("%s: loading ipam.json: %v", o.Name, err)
	}
	return registry.Accounting()
}

// ExpectUTC fails the test if any rendered file holds an RFC 3339 timestamp that is not in
// UTC (does not end in Z). It scans every JSON string that parses as a time.
//
// Example:
//
//	w.ExpectUTC(out) // every timestamp in the output is UTC
func (w *W) ExpectUTC(o *Out) {
	w.T.Helper()
	for path, data := range o.Files {
		if !strings.HasSuffix(path, ".json") {
			continue
		}
		var v any
		if json.Unmarshal(data, &v) != nil {
			continue
		}
		if bad := nonUTC(v); bad != "" {
			w.Fatalf("%s/%s: timestamp %q is not UTC", o.Name, path, bad)
		}
	}
}

// nonUTC returns the first string in a JSON value that is a timestamp not written in UTC.
func nonUTC(v any) string {
	switch t := v.(type) {
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil && !strings.HasSuffix(t, "Z") {
			_ = parsed
			return t
		}
	case []any:
		for _, item := range t {
			if bad := nonUTC(item); bad != "" {
				return bad
			}
		}
	case map[string]any:
		for _, item := range t {
			if bad := nonUTC(item); bad != "" {
				return bad
			}
		}
	}
	return ""
}

// ExpectNoSecrets fails the test if a secret value appears in any rendered file or in the
// error text of the plan.
//
// Example:
//
//	w.ExpectNoSecrets(out, "hunter2") // the value appears in no output and no error
func (w *W) ExpectNoSecrets(o *Out, secrets ...string) {
	w.T.Helper()
	for _, secret := range secrets {
		if o.Err != nil && strings.Contains(o.Err.Error(), secret) {
			w.Fatalf("%s: the error text echoes a secret", o.Name)
		}
		for path, data := range o.Files {
			if bytes.Contains(data, []byte(secret)) {
				w.Fatalf("%s: %s holds a secret", o.Name, path)
			}
		}
	}
}
