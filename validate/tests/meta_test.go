package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/validate"
)

// ruleTests reads the test files at the paths and returns, for each test function named
// TestRule_<something>, the rule IDs its body mentions as string literals. The function's
// name must start with TestRule_ and the ID with underscores for dashes; a function whose
// body never names the ID does not count as a test of that rule.
func ruleTests(files map[string]string) map[string][]string {
	out := map[string][]string{}
	fset := token.NewFileSet()
	for name, src := range files {
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(fn.Name.Name, "TestRule_") || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if text, err := strconv.Unquote(lit.Value); err == nil {
						out[fn.Name.Name] = append(out[fn.Name.Name], text)
					}
				}
				return true
			})
		}
	}
	return out
}

// untested returns the IDs of the rules that have no test that fires them: no function named
// TestRule_<ID> whose body names the ID. Following the plan, a rule's test plants the fault
// and checks the rule fires; this checks the test exists, and that it is about this rule.
func untested(rules []validate.Rule, files map[string]string) []string {
	tests := ruleTests(files)
	var missing []string
	for _, rule := range rules {
		name := "TestRule_" + strings.ReplaceAll(rule.ID, "-", "_")
		mentioned := false
		for _, text := range tests[name] {
			mentioned = mentioned || text == rule.ID
		}
		if !mentioned {
			missing = append(missing, rule.ID)
		}
	}
	sort.Strings(missing)
	return missing
}

// testSources reads the test files of this directory.
func testSources(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob("*_test.go")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no test files found: %v", err)
	}
	files := map[string]string{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = string(data)
	}
	return files
}

// P1-VAL-054
//
// TestEveryRuleHasAFiringTest walks the catalogue and fails for each rule that has no test
// that fires it. Adding a rule without its test is a failing build.
func TestEveryRuleHasAFiringTest(t *testing.T) {
	if missing := untested(validate.Rules(), testSources(t)); len(missing) != 0 {
		t.Errorf("rules with no firing test (TestRule_<ID> naming the ID): %v", missing)
	}
}

// TestTheMetaTestCatchesAMissingTest proves the checker works: a rule with no test is named, a
// test that does not name its own rule does not count, and one that does is accepted.
func TestTheMetaTestCatchesAMissingTest(t *testing.T) {
	rules := []validate.Rule{{ID: "V-X-001"}, {ID: "V-X-002"}, {ID: "V-X-003"}}
	files := map[string]string{
		"a_test.go": `package tests
func TestRule_V_X_001(t *testing.T) { fires(t, p, "V-X-001") }
func TestRule_V_X_002(t *testing.T) { fires(t, p, "V-X-001") }
func TestSomethingElse(t *testing.T) { fires(t, p, "V-X-003") }
`,
	}
	got := untested(rules, files)
	if strings.Join(got, ",") != "V-X-002,V-X-003" {
		t.Errorf("untested %v, want V-X-002 and V-X-003: 002's test names the wrong rule, 003 has no TestRule_ function", got)
	}
	if got := untested(rules, map[string]string{"bad_test.go": "not go"}); len(got) != 3 {
		t.Errorf("unparseable sources: %v", got)
	}
}

// TestEveryRuleHasATitleAndAUniqueID checks the catalogue itself: IDs are unique, follow the
// V-<FAMILY>-<NNN> form, and each has a title.
func TestEveryRuleHasATitleAndAUniqueID(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range validate.Rules() {
		if seen[r.ID] {
			t.Errorf("%s is registered twice", r.ID)
		}
		seen[r.ID] = true
		parts := strings.Split(r.ID, "-")
		if len(parts) != 3 || parts[0] != "V" || len(parts[2]) != 3 || r.Title == "" {
			t.Errorf("rule %+v is malformed", r)
		}
	}
	for _, id := range []string{
		"V-ADDR-001", "V-ADDR-002", "V-ADDR-003", "V-ADDR-004", "V-ADDR-005", "V-ADDR-006", "V-ADDR-007", "V-ADDR-008",
		"V-TOPO-001", "V-TOPO-002", "V-TOPO-003", "V-TOPO-004", "V-TOPO-020", "V-LINK-001", "V-LINK-002", "V-LINK-003", "V-LINK-004",
		"V-ROUTE-001", "V-ROUTE-002", "V-ROUTE-003", "V-ROUTE-004", "V-ROUTE-005", "V-ROUTE-006",
		"V-CFG-001", "V-CFG-002", "V-CFG-003", "V-CFG-004", "V-CFG-005", "V-CFG-006", "V-SIM-001", "V-SIM-002", "V-SIM-003",
	} {
		if !seen[id] {
			t.Errorf("the plan's catalogue has %s and the code does not", id)
		}
	}
}
