package tests

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/config"
	"github.com/vectorihub/netctl/internal/jsonschema"
)

// schemaValidator returns a validator for the published schema.
func schemaValidator(t testing.TB) *jsonschema.Schema {
	t.Helper()
	v, err := jsonschema.New(config.SchemaJSON())
	if err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	return v
}

// TestSchemaFileIsTheEmbeddedSchema checks the file published in the repository is
// byte for byte what the package hands out, and that a caller changing the slice it
// got does not change the next one.
func TestSchemaFileIsTheEmbeddedSchema(t *testing.T) {
	file, err := os.ReadFile("../schema/v1alpha1.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(file) != string(config.SchemaJSON()) {
		t.Error("config/schema/v1alpha1.json is not what SchemaJSON returns")
	}
	got := config.SchemaJSON()
	got[0] = 'X'
	if config.SchemaJSON()[0] != '{' {
		t.Error("SchemaJSON returned the package's own slice")
	}
}

// TestSchemaUsesOnlyKeywordsTheValidatorKnows checks the schema holds nothing the
// test validator would silently ignore.
func TestSchemaUsesOnlyKeywordsTheValidatorKnows(t *testing.T) {
	if bad := schemaValidator(t).UnsupportedKeywords(); len(bad) != 0 {
		t.Errorf("keywords the validator ignores: %v", bad)
	}
}

// TestEveryGeneratedConfigMatchesTheSchema is P1-CFG-012: every config of every
// plan we can generate, healthy and failed over, validates against the JSON Schema.
func TestEveryGeneratedConfigMatchesTheSchema(t *testing.T) {
	v := schemaValidator(t)
	check := func(label string, c *config.NodeConfig) {
		t.Helper()
		data, err := c.Encode()
		if err != nil {
			t.Fatal(err)
		}
		problems, err := v.Validate(data)
		if err != nil || len(problems) != 0 {
			t.Errorf("%s: %v %v\n%s", label, err, problems, data)
		}
	}
	for _, up := range []bool{true, false} {
		for node, c := range threeSite(t, up) {
			check(string(node), c)
		}
	}
	for run := 0; run < 100; run++ {
		for _, c := range randomPlan(t, run).configs {
			check(string(c.Node), c)
		}
	}
}

// TestValidatorRejectsBadConfigs checks the validator, and so the schema, refuses
// each kind of fault: otherwise "everything validates" would prove nothing.
func TestValidatorRejectsBadConfigs(t *testing.T) {
	v := schemaValidator(t)
	good, err := threeSite(t, true)[builder].Encode()
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal(good, &base); err != nil {
		t.Fatal(err)
	}
	clone := func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal(good, &m)
		return m
	}
	peer := func(m map[string]any, i int) map[string]any { return m["peers"].([]any)[i].(map[string]any) }
	for name, tc := range map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"unknown key":         {func(m map[string]any) { m["interface"] = "wg0" }, `"interface"`},
		"unknown peer key":    {func(m map[string]any) { peer(m, 0)["ifname"] = "wg0" }, `"ifname"`},
		"missing key":         {func(m map[string]any) { delete(m, "peers") }, `"peers"`},
		"wrong schema":        {func(m map[string]any) { m["schema"] = "x" }, "must be"},
		"bad role":            {func(m map[string]any) { m["role"] = "router" }, "not one of"},
		"bad peer role":       {func(m map[string]any) { peer(m, 0)["role"] = "boss" }, "not one of"},
		"port out of range":   {func(m map[string]any) { m["overlay"].(map[string]any)["listen_port"] = 70000.0 }, "above"},
		"fractional number":   {func(m map[string]any) { peer(m, 0)["keepalive_seconds"] = 2.5 }, "must be integer"},
		"negative keepalive":  {func(m map[string]any) { peer(m, 0)["keepalive_seconds"] = -1.0 }, "below"},
		"bad prefix":          {func(m map[string]any) { m["advertise"] = []any{"not a prefix"} }, "does not match"},
		"prefix twice":        {func(m map[string]any) { m["advertise"] = []any{"10.0.0.0/8", "10.0.0.0/8"} }, "twice"},
		"bad hash":            {func(m map[string]any) { m["hash"] = "md5:abc" }, "does not match"},
		"bad plan id":         {func(m map[string]any) { m["plan_id"] = "three-site" }, "does not match"},
		"null for a list":     {func(m map[string]any) { m["routes"] = nil }, "must be array"},
		"string for a bool":   {func(m map[string]any) { m["forwarding"] = "yes" }, "must be boolean"},
		"bad node name":       {func(m map[string]any) { m["node"] = "Builder 1" }, "does not match"},
		"bad link":            {func(m map[string]any) { peer(m, 0)["link"] = "a-b" }, "does not match"},
		"bad public key ref":  {func(m map[string]any) { peer(m, 0)["public_key_ref"] = "MFswDQYJKoZIhvcNAQEBBQADSgAwRwJAbk=" }, "does not match"},
		"bad policy action":   {func(m map[string]any) { m["policy"] = []any{map[string]any{"action": "maybe", "from": "a", "to": "b"}} }, "not one of"},
		"zero fail threshold": {func(m map[string]any) { m["health"].(map[string]any)["fail_threshold"] = 0.0 }, "below"},
		"bad probe target": {func(m map[string]any) {
			m["health"].(map[string]any)["probes"] = []any{map[string]any{"link": "a~b", "target": "x y", "interval_seconds": 5.0}}
		}, "does not match"},
		"route without active": {func(m map[string]any) { delete(m["routes"].([]any)[0].(map[string]any), "active") }, `"active"`},
	} {
		m := clone()
		tc.mutate(m)
		data, _ := json.Marshal(m)
		problems, err := v.Validate(data)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), tc.want) {
			t.Errorf("%s: problems %v, want one mentioning %q", name, problems, tc.want)
		}
	}
	if problems, _ := v.Validate(good); len(problems) != 0 {
		t.Errorf("the good config does not validate: %v", problems)
	}
}

// jsonTags collects, for the struct type and everything it holds, the JSON key of
// each field, by the name of the struct that holds it.
func jsonTags(t reflect.Type, out map[string][]string) {
	for t.Kind() == reflect.Slice || t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t.PkgPath() != "github.com/vectorihub/netctl/config" {
		return
	}
	if _, done := out[t.Name()]; done {
		return
	}
	out[t.Name()] = nil
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		out[t.Name()] = append(out[t.Name()], name)
		jsonTags(f.Type, out)
	}
}

// TestSchemaAndTypesAgree checks the Go structs and the JSON Schema list the same
// keys at every level, and the same ones are required, so neither can drift without
// a test failing: a field in the types and not in the schema would be refused by
// every consumer, and one in the schema and not in the types could never be read.
func TestSchemaAndTypesAgree(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(config.SchemaJSON(), &doc); err != nil {
		t.Fatal(err)
	}
	defs := doc["$defs"].(map[string]any)
	tags := map[string][]string{}
	jsonTags(reflect.TypeOf(config.NodeConfig{}), tags)
	// Each Go struct, with the schema node that describes it.
	nodes := map[string]map[string]any{
		"NodeConfig":  doc,
		"Overlay":     doc["properties"].(map[string]any)["overlay"].(map[string]any),
		"Peer":        defs["peer"].(map[string]any),
		"Probe":       defs["peer"].(map[string]any)["properties"].(map[string]any)["probe"].(map[string]any),
		"Route":       defs["route"].(map[string]any),
		"Health":      doc["properties"].(map[string]any)["health"].(map[string]any),
		"ProbeTarget": defs["probe_target"].(map[string]any),
		"PolicyEntry": defs["policy_entry"].(map[string]any),
	}
	for name, keys := range tags {
		node, ok := nodes[name]
		if !ok {
			t.Errorf("the struct %s has no schema node in this test", name)
			continue
		}
		props := node["properties"].(map[string]any)
		var inSchema []string
		for k := range props {
			inSchema = append(inSchema, k)
		}
		if !sameSet(keys, inSchema) {
			t.Errorf("%s: types have %v, schema has %v", name, keys, inSchema)
		}
		omit := map[string]bool{}
		typ := structByName(name)
		for i := 0; i < typ.NumField(); i++ {
			if strings.Contains(typ.Field(i).Tag.Get("json"), "omitempty") {
				omit[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
			}
		}
		var required []string
		for _, r := range node["required"].([]any) {
			required = append(required, r.(string))
		}
		var mustHave []string
		for _, k := range keys {
			if !omit[k] {
				mustHave = append(mustHave, k)
			}
		}
		if !sameSet(required, mustHave) {
			t.Errorf("%s: schema requires %v, types always write %v", name, required, mustHave)
		}
	}
}

// structByName returns the config struct type with the name.
func structByName(name string) reflect.Type {
	for _, v := range []any{config.NodeConfig{}, config.Overlay{}, config.Peer{}, config.Probe{}, config.Route{}, config.Health{}, config.ProbeTarget{}, config.PolicyEntry{}} {
		if t := reflect.TypeOf(v); t.Name() == name {
			return t
		}
	}
	return nil
}

// sameSet reports whether two lists hold the same strings, in any order.
func sameSet(a, b []string) bool {
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return len(a) == len(b)
}
