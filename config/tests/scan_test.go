package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vectorihub/netctl/config"
)

// ruleNames returns the rules of the findings, sorted by the scanner's own order.
func ruleNames(findings []config.Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Rule)
	}
	return out
}

// P1-CFG-030
//
// TestNoKeyMaterialInGeneratedConfigs checks no config we can generate, written as it
// would be saved, has anything with the shape of a key, token or secret: not in the
// three-site plans and not in any of the random ones.
func TestNoKeyMaterialInGeneratedConfigs(t *testing.T) {
	check := func(label string, c *config.NodeConfig) {
		data, err := c.Clone().Encode()
		if err != nil {
			t.Fatal(err)
		}
		if findings := config.ScanSecrets(data); len(findings) != 0 {
			t.Errorf("%s: %v", label, findings)
		}
	}
	for _, up := range []bool{true, false} {
		for node, c := range threeSite(t, up) {
			check(string(node), c)
		}
	}
	for seed := 0; seed < 200; seed++ {
		for _, c := range randomPlan(t, seed).configs {
			check(string(c.Node), c)
		}
	}
}

// TestScannerFindsPlantedSecrets checks every rule fires on its own kind of secret,
// so a clean scan of the real configs means something.
func TestScannerFindsPlantedSecrets(t *testing.T) {
	wgKey := "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=" // the example key from the WireGuard documentation
	for name, tc := range map[string]struct {
		doc  string
		rule string
	}{
		"wireguard key as a value":      {`{"public_key_ref": "` + wgKey + `"}`, "wireguard-key"},
		"wireguard key inside a string": {`{"note": "key=` + wgKey + ` ok"}`, "wireguard-key"},
		"key named private":             {`{"private_key": "x"}`, "secret-key-name"},
		"key named preshared":           {`{"peers": [{"preshared_key": "x"}]}`, "secret-key-name"},
		"key named psk":                 {`{"PSK": "x"}`, "secret-key-name"},
		"key named token":               {`{"auth_token": "x"}`, "secret-key-name"},
		"key named password":            {`{"password": "x"}`, "secret-key-name"},
		"pem block":                     {`{"x": "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----"}`, "pem-block"},
		"hex key":                       {`{"x": "` + strings.Repeat("ab", 32) + `"}`, "hex-key"},
		"jwt":                           {`{"x": "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJl"}`, "jwt"},
		"github token":                  {`{"x": "ghp_abcdefghijklmnopqrstuvwxyz0123456789"}`, "access-token"},
		"aws key":                       {`{"x": "AKIAIOSFODNN7EXAMPLE"}`, "access-token"},
		"long mixed token":              {`{"x": "Zm9vYmFyQmF6MTIzNDU2Nzg5MEFiQ2RFZkdoSWpLbE1u"}`, "long-token"},
		"plain text with a pem block":   {"-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blbn", "pem-block"},
	} {
		findings := config.ScanSecrets([]byte(tc.doc))
		if !strings.Contains(strings.Join(ruleNames(findings), ","), tc.rule) {
			t.Errorf("%s: findings %v, want rule %s", name, findings, tc.rule)
		}
		for _, f := range findings {
			if strings.Contains(f.String(), wgKey) || strings.Contains(f.String(), "ghp_abc") {
				t.Errorf("%s: the finding repeats the secret: %s", name, f)
			}
		}
	}
}

// TestScannerLeavesLegitimateValuesAlone checks what a config is made of is not
// flagged: names, link IDs, addresses, prefixes, the hash field, and long node names.
func TestScannerLeavesLegitimateValuesAlone(t *testing.T) {
	hash := "sha256:" + strings.Repeat("ab", 32)
	doc := `{
		"hash": "` + hash + `",
		"node": "a-very-long-node-name-that-goes-on-and-on-1234567890",
		"link": "builder-1~dgx-spark", "public_key_ref": "node:aws-hub",
		"prefix": "10.42.16.0/20", "v6": "fd00:1234:5678::/48", "endpoint": "203.0.113.10:51820",
		"plan_id": "three-site@7f3c9e1a", "numbers": [1, 25, 51820]
	}`
	if findings := config.ScanSecrets([]byte(doc)); len(findings) != 0 {
		t.Errorf("flagged what belongs: %v", findings)
	}
	// The same hash anywhere but the hash field is not allowed to hide.
	moved := `{"note": "` + strings.TrimPrefix(hash, "sha256:") + `"}`
	if findings := config.ScanSecrets([]byte(moved)); len(findings) == 0 {
		t.Error("a 64 digit hex string outside the hash field was not flagged")
	}
	if findings := config.ScanSecrets([]byte(`{"nested": {"hash": "` + strings.TrimPrefix(hash, "sha256:") + `"}}`)); len(findings) == 0 {
		t.Error("a bare hex string under a nested hash key was not flagged")
	}
}

// TestScannerOrderIsFixed checks the findings come in the same order every time,
// whatever the order the keys were written in.
func TestScannerOrderIsFixed(t *testing.T) {
	doc := []byte(`{"z_token": "1", "a_secret": "2", "m": {"password": "3"}, "list": [{"psk": "4"}]}`)
	first := config.ScanSecrets(doc)
	if len(first) != 4 {
		t.Fatalf("findings %v", first)
	}
	for i := 0; i < 20; i++ {
		again := config.ScanSecrets(doc)
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("the order changed: %v and %v", first, again)
			}
		}
	}
	if first[0].Path != "a_secret" || first[3].Path != "z_token" {
		t.Errorf("not in key order: %v", first)
	}
}

// keysOf collects every object key in the JSON.
func keysOf(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	var rec func(v any)
	rec = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				keys[k] = true
				rec(child)
			}
		case []any:
			for _, child := range v {
				rec(child)
			}
		}
	}
	rec(doc)
	return keys
}

// schemaVocabulary collects every property name the schema allows, at any depth.
func schemaVocabulary(t *testing.T) map[string]bool {
	t.Helper()
	var doc any
	if err := json.Unmarshal(config.SchemaJSON(), &doc); err != nil {
		t.Fatal(err)
	}
	vocab := map[string]bool{}
	var rec func(v any)
	rec = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if props, ok := v["properties"].(map[string]any); ok {
				for name := range props {
					vocab[name] = true
				}
			}
			for _, child := range v {
				rec(child)
			}
		case []any:
			for _, child := range v {
				rec(child)
			}
		}
	}
	rec(doc)
	return vocab
}

// P1-CFG-020
//
// TestNoOSSpecificFields checks no generated config has a key outside the schema's
// vocabulary (an allow-list, so a new key has to be added to the schema on purpose),
// and none has a value that belongs to a machine: an interface name, a path, a
// sysctl, a command or a table number.
func TestNoOSSpecificFields(t *testing.T) {
	vocab := schemaVocabulary(t)
	check := func(label string, c *config.NodeConfig) {
		data, err := c.Clone().Encode()
		if err != nil {
			t.Fatal(err)
		}
		for key := range keysOf(t, data) {
			if !vocab[key] {
				t.Errorf("%s: the key %q is not in the schema's vocabulary", label, key)
			}
		}
		if findings := config.ScanOSFields(data); len(findings) != 0 {
			t.Errorf("%s: %v", label, findings)
		}
	}
	for _, up := range []bool{true, false} {
		for node, c := range threeSite(t, up) {
			check(string(node), c)
		}
	}
	for seed := 0; seed < 200; seed++ {
		for _, c := range randomPlan(t, seed).configs {
			check(string(c.Node), c)
		}
	}
}

// TestOSScannerFindsPlantedFields checks every rule of the OS scanner fires.
func TestOSScannerFindsPlantedFields(t *testing.T) {
	for name, tc := range map[string]struct {
		doc  string
		rule string
	}{
		"interface key":     {`{"interface": "x"}`, "os-key-name"},
		"table key":         {`{"peers": [{"table": 100}]}`, "os-key-name"},
		"sysctl key":        {`{"sysctl": {}}`, "os-key-name"},
		"interface name":    {`{"x": "wg0"}`, "interface-name"},
		"interface in text": {`{"x": "bring up eth0 now"}`, "interface-name"},
		"unix path":         {`{"x": "/etc/wireguard/wg0.conf"}`, "file-path"},
		"windows path":      {`{"x": "C:\\Program Files\\wg"}`, "file-path"},
		"conf file":         {`{"x": "peer.conf"}`, "file-path"},
		"sysctl value":      {`{"x": "net.ipv4.ip_forward=1"}`, "sysctl"},
		"command":           {`{"x": "ip route add 10.0.0.0/8 dev wg0"}`, "command"},
		"wg command":        {`{"x": "wg set wg0 peer"}`, "command"},
		"table number":      {`{"x": "ip rule add table 100"}`, "table-number"},
	} {
		findings := config.ScanOSFields([]byte(tc.doc))
		if !strings.Contains(strings.Join(ruleNames(findings), ","), tc.rule) {
			t.Errorf("%s: findings %v, want rule %s", name, findings, tc.rule)
		}
	}
	for _, ok := range []string{`{"node": "builder-1", "link": "a~b", "prefix": "10.0.0.0/8", "x": "node:aws-hub", "ep": "203.0.113.10:51820"}`} {
		if findings := config.ScanOSFields([]byte(ok)); len(findings) != 0 {
			t.Errorf("flagged what belongs: %v", findings)
		}
	}
}
