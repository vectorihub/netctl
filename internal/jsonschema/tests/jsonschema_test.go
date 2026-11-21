package tests

import (
	"strings"
	"testing"

	"github.com/vectorihub/netctl/internal/jsonschema"
)

// schema is a small schema that uses every keyword the validator supports.
const schema = `{
  "type": "object", "additionalProperties": false, "required": ["name", "kind"],
  "properties": {
    "name": {"type": "string", "pattern": "^[a-z]+$", "minLength": 2},
    "kind": {"enum": ["a", "b"]},
    "fixed": {"const": "x"},
    "count": {"type": "integer", "minimum": 1, "maximum": 5},
    "on": {"type": "boolean"},
    "tags": {"type": "array", "items": {"$ref": "#/$defs/tag"}, "uniqueItems": true},
    "sub": {"type": "object", "properties": {"n": {"type": "integer"}}}
  },
  "$defs": {"tag": {"type": "string", "minLength": 1}}
}`

// check validates a document against the schema, failing the test on a JSON error.
func check(t *testing.T, doc string) string {
	t.Helper()
	s, err := jsonschema.New([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	problems, err := s.Validate([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(problems, "\n")
}

// TestAValidDocumentHasNoProblems checks a document that satisfies every keyword.
func TestAValidDocumentHasNoProblems(t *testing.T) {
	if got := check(t, `{"name":"ab","kind":"a","fixed":"x","count":3,"on":true,"tags":["p","q"],"sub":{"n":1}}`); got != "" {
		t.Errorf("problems: %s", got)
	}
}

// TestEachKeywordIsEnforced checks every supported keyword refuses what it should, with
// the path of the fault in the message.
func TestEachKeywordIsEnforced(t *testing.T) {
	for name, tc := range map[string]struct{ doc, want string }{
		"required":             {`{"name":"ab"}`, `is missing "kind"`},
		"additionalProperties": {`{"name":"ab","kind":"a","extra":1}`, `the key "extra"`},
		"type string":          {`{"name":5,"kind":"a"}`, "$.name: must be string"},
		"type integer":         {`{"name":"ab","kind":"a","count":2.5}`, "must be integer"},
		"type boolean":         {`{"name":"ab","kind":"a","on":"yes"}`, "must be boolean"},
		"type array":           {`{"name":"ab","kind":"a","tags":"x"}`, "must be array"},
		"type object":          {`{"name":"ab","kind":"a","sub":[]}`, "must be object"},
		"pattern":              {`{"name":"AB","kind":"a"}`, "does not match"},
		"minLength":            {`{"name":"a","kind":"a"}`, "is shorter than"},
		"enum":                 {`{"name":"ab","kind":"c"}`, "is not one of"},
		"const":                {`{"name":"ab","kind":"a","fixed":"y"}`, "must be x"},
		"minimum":              {`{"name":"ab","kind":"a","count":0}`, "is below"},
		"maximum":              {`{"name":"ab","kind":"a","count":6}`, "is above"},
		"uniqueItems":          {`{"name":"ab","kind":"a","tags":["p","p"]}`, "twice"},
		"items and $ref":       {`{"name":"ab","kind":"a","tags":["p",""]}`, "$.tags[1]"},
		"nested path":          {`{"name":"ab","kind":"a","sub":{"n":"x"}}`, "$.sub.n"},
	} {
		if got := check(t, tc.doc); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q, want it to contain %q", name, got, tc.want)
		}
	}
}

// TestProblemsAreSortedAndComplete checks every violation is reported, in a fixed order.
func TestProblemsAreSortedAndComplete(t *testing.T) {
	got := strings.Split(check(t, `{"name":1,"kind":"z","count":9,"x":1}`), "\n")
	if len(got) != 4 {
		t.Fatalf("%d problems, want 4: %v", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("not sorted: %v", got)
		}
	}
}

// TestUnsupportedKeywordsAreListed checks a keyword the validator would ignore is named,
// with its place, and the supported ones are not.
func TestUnsupportedKeywordsAreListed(t *testing.T) {
	s, err := jsonschema.New([]byte(schema))
	if err != nil || len(s.UnsupportedKeywords()) != 0 {
		t.Fatalf("the supported schema lists %v (%v)", s.UnsupportedKeywords(), err)
	}
	odd, err := jsonschema.New([]byte(`{"type":"object","properties":{"a":{"type":"string","format":"email"}},"oneOf":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(odd.UnsupportedKeywords(), ",")
	if !strings.Contains(got, "format") || !strings.Contains(got, "oneOf") {
		t.Errorf("unsupported %q", got)
	}
}

// TestBadInputIsAnError checks text that is not JSON is an error, for the schema and for
// the document.
func TestBadInputIsAnError(t *testing.T) {
	if _, err := jsonschema.New([]byte(`{`)); err == nil {
		t.Error("a schema that is not JSON was accepted")
	}
	s, _ := jsonschema.New([]byte(schema))
	if _, err := s.Validate([]byte(`{`)); err == nil {
		t.Error("a document that is not JSON was accepted")
	}
}
