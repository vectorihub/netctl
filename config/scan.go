package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Finding is something a scanner found in a config.
//
// It names where and which rule, and never repeats the value: a scanner that
// copied a leaked key into its report would only leak it again.
//
// Example:
//
//	Finding{Path: "peers[0].public_key_ref", Rule: "wireguard-key", Why: "a 44 character base64 string, which is what a WireGuard key looks like"}.
type Finding struct {
	// Path is where it was found: keys joined by dots, with list positions in
	// square brackets.
	//
	// Example:
	//
	//	"peers[0].public_key_ref".
	Path string `json:"path"`
	// Rule is the short name of the rule that matched.
	//
	// Example:
	//
	//	"private-key-name".
	Rule string `json:"rule"`
	// Why says in words what looked wrong.
	//
	// Example:
	//
	//	"the key is named like a secret".
	Why string `json:"why"`
}

// String writes the finding on one line.
//
// Example:
//
//	"peers[0].public_key_ref: wireguard-key: a 44 character base64 string, ...".
func (f Finding) String() string { return f.Path + ": " + f.Rule + ": " + f.Why }

// walk calls visit for every key and every string value of the JSON in data, in a
// fixed order: keys sorted, lists in order. For a key it passes isKey true and the
// key as text; for a string value, isKey false. Text that is not JSON is visited
// whole, as one value at the empty path.
func walk(data []byte, visit func(path, text string, isKey bool)) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		visit("", string(data), false)
		return
	}
	var rec func(path string, v any)
	rec = func(path string, v any) {
		switch v := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				child := k
				if path != "" {
					child = path + "." + k
				}
				visit(child, k, true)
				rec(child, v[k])
			}
		case []any:
			for i, item := range v {
				rec(fmt.Sprintf("%s[%d]", path, i), item)
			}
		case string:
			visit(path, v, false)
		}
	}
	rec("", doc)
}

// secretKeyWords are parts of a key's name that say its value is a secret.
var secretKeyWords = []string{"private", "preshared", "pre_shared", "psk", "secret", "password", "passwd", "token", "credential", "api_key", "apikey", "bearer"}

var (
	// wireGuardKey is a 32 byte key in base64: 43 characters and a padding sign.
	wireGuardKey = regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`)
	// hex64 is 32 bytes in hex, the size of a key or a SHA-256.
	hex64 = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	// jwt is a JSON Web Token: three dot separated base64url parts.
	jwt = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*$`)
	// tokenPrefix is the start of a well known kind of access token or key.
	tokenPrefix = regexp.MustCompile(`^(ghp_|gho_|ghs_|ghu_|github_pat_|glpat-|xox[abprs]-|AKIA[0-9A-Z]{16}|sk-[A-Za-z0-9]{16,})`)
	// longToken is a long run of characters used by encoded secrets.
	longToken = regexp.MustCompile(`^[A-Za-z0-9+/_=-]{32,}$`)
	// wordBreak splits a value into the runs of characters a secret is made of.
	wordBreak = regexp.MustCompile(`[^A-Za-z0-9+/_=.-]+`)
	// hashValue is the one place a 64 digit hex string belongs: the hash field.
	hashValue = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// looksEncoded reports whether the text mixes upper case, lower case and digits,
// as an encoded secret does and a node name, which is lower case, does not.
func looksEncoded(text string) bool {
	var upper, lower, digit bool
	for _, r := range text {
		switch {
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= '0' && r <= '9':
			digit = true
		}
	}
	return upper && lower && digit
}

// candidates splits a value into the runs that could be a secret. An equals sign is
// both the padding of base64 and the sign in key=value, so each run is also tried
// from just after every equals sign that is not at its end.
func candidates(text string) []string {
	var out []string
	for _, word := range wordBreak.Split(text, -1) {
		out = append(out, word)
		for i := 0; i < len(word)-1; i++ {
			if word[i] == '=' {
				out = append(out, word[i+1:])
			}
		}
	}
	return out
}

// ScanSecrets looks through a config, as written, for anything that looks like
// secret material: a key named like a secret (private, preshared, psk, secret,
// password, token, credential, bearer), a PEM block, a WireGuard key (a 32 byte
// base64 string), a 64 digit hex string, a JSON Web Token, a well known access
// token, or a long encoded run of characters. A config refers to a peer's public
// key by name and holds none, so even a public key is a finding; the hash field's
// "sha256:" value is the one hex string that belongs, and is not.
//
// It reads JSON, or any text. It returns the findings in a fixed order and
// repeats none of the values. No findings does not prove there is no secret, only
// that nothing has the shape of one: the guarantee is that a generated config is
// built from names, addresses and numbers, and the scan checks it stays so.
//
// Example:
//
//	if findings := config.ScanSecrets(data); len(findings) > 0 { /* refuse to write it */ }
func ScanSecrets(data []byte) []Finding {
	var out []Finding
	walk(data, func(path, text string, isKey bool) {
		if isKey {
			lower := strings.ToLower(text)
			for _, word := range secretKeyWords {
				if strings.Contains(lower, word) {
					out = append(out, Finding{path, "secret-key-name", "the key is named like a secret"})
					break
				}
			}
			return
		}
		if path == "hash" && hashValue.MatchString(text) {
			return
		}
		if strings.Contains(text, "-----BEGIN") {
			out = append(out, Finding{path, "pem-block", "a PEM block, which holds a key or certificate"})
		}
		for _, word := range candidates(text) {
			switch {
			case wireGuardKey.MatchString(word):
				out = append(out, Finding{path, "wireguard-key", "a 44 character base64 string, which is what a WireGuard key looks like"})
			case hex64.MatchString(word):
				out = append(out, Finding{path, "hex-key", "64 hex digits, the size of a 32 byte key"})
			case jwt.MatchString(word):
				out = append(out, Finding{path, "jwt", "a JSON Web Token"})
			case tokenPrefix.MatchString(word):
				out = append(out, Finding{path, "access-token", "the start of a well known access token"})
			case longToken.MatchString(word) && looksEncoded(word):
				out = append(out, Finding{path, "long-token", "a long run of mixed-case encoded characters"})
			}
		}
	})
	return out
}

// osKeyNames are keys that only make sense for a particular machine.
var osKeyNames = map[string]bool{
	"interface": true, "iface": true, "ifname": true, "interface_name": true, "device": true,
	"table": true, "table_id": true, "rt_table": true, "rt_proto": true, "protocol_number": true,
	"sysctl": true, "sysctls": true, "path": true, "file": true, "filename": true, "dir": true,
	"command": true, "cmd": true, "exec": true, "unit": true, "service": true, "systemd": true,
	"nft": true, "nftables": true, "iptables": true, "netlink": true, "mtu": true, "fwmark": true,
}

var (
	// interfaceName is a Linux style network interface name.
	interfaceName = regexp.MustCompile(`^(wg\d+|eth\d+|ens\d+|enp\w+|eno\d+|wlan\d+|wlp\w+|tun\d+|tap\d+|veth\w*|docker\d+|br-[0-9a-f]+|lo)$`)
	// filePath is an absolute path on Unix or Windows, or a configuration file.
	filePath = regexp.MustCompile(`^(/[A-Za-z0-9._-]+(/|$)|[A-Za-z]:\\)|\.(conf|service|sh|toml|ini)$`)
	// sysctlName is a kernel setting.
	sysctlName = regexp.MustCompile(`\b(net|kernel|vm)\.(ipv4|ipv6|core|netfilter|ip_forward)\b`)
	// commandLine is a call to a network tool with a sub-command.
	commandLine = regexp.MustCompile(`\b(ip|wg|nft|iptables|ifconfig|sysctl|systemctl|route)\s+(-[a-z]|link|addr|address|route|rule|set|add|del|show|flush|enable|start)\b`)
	// tableNumber is a Linux routing table number.
	tableNumber = regexp.MustCompile(`\btable\s+\d+\b`)
)

// ScanOSFields looks through a config, as written, for anything that belongs to a
// particular machine and not to Beaver's vocabulary: a key such as interface,
// table, sysctl, path or command; an interface name such as wg0 or eth0; a file
// path; a sysctl name such as net.ipv4.ip_forward; a command such as "ip route
// add"; or a routing-table number. The agent chooses all of these; the config says
// what, never how.
//
// It reads JSON, or any text, and returns the findings in a fixed order.
//
// Example:
//
//	if findings := config.ScanOSFields(data); len(findings) > 0 { /* the config says how */ }
func ScanOSFields(data []byte) []Finding {
	var out []Finding
	walk(data, func(path, text string, isKey bool) {
		if isKey {
			if osKeyNames[strings.ToLower(text)] {
				out = append(out, Finding{path, "os-key-name", "the key names something that belongs to a machine"})
			}
			return
		}
		if filePath.MatchString(text) {
			out = append(out, Finding{path, "file-path", "a file path"})
		}
		if sysctlName.MatchString(text) {
			out = append(out, Finding{path, "sysctl", "a kernel setting"})
		}
		if commandLine.MatchString(text) {
			out = append(out, Finding{path, "command", "a command line"})
		}
		if tableNumber.MatchString(text) {
			out = append(out, Finding{path, "table-number", "a routing table number"})
		}
		for _, word := range strings.Fields(text) {
			if interfaceName.MatchString(word) {
				out = append(out, Finding{path, "interface-name", "a network interface name"})
				break
			}
		}
	})
	return out
}
