package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// goPackage is the part of `go list -json` output the guard needs.
type goPackage struct {
	ImportPath string
	Name       string
	Standard   bool
	Imports    []string
	Module     *struct {
		Main bool
	}
}

// bannedAnywhere lists packages that must not appear anywhere in netctl's
// import graph, direct or transitive. Each one runs programs, talks to the
// network, opens a database or drives the operating system, which is the
// agent's job and never the planner's.
var bannedAnywhere = []string{
	"os/exec",
	"golang.org/x/sys",
	"net/http",
	"database/sql",
	"github.com/vishvananda/netlink",
	"golang.zx2c4.com/wireguard",
	"github.com/google/nftables",
}

// bannedDirect lists packages that library code must not import itself. They
// are not banned transitively because the standard library (os, time) uses
// syscall internally, so every package that imports fmt reaches it.
var bannedDirect = []string{
	"syscall",
}

// matches reports whether path is banned or lives beneath it.
func matches(path, banned string) bool {
	return path == banned || strings.HasPrefix(path, banned+"/")
}

// violations returns one human-readable line per rule broken by packages, sorted
// so the output is stable. packages is the full dependency listing of the module.
func violations(packages []goPackage) []string {
	var problems []string
	for _, pkg := range packages {
		for _, banned := range bannedAnywhere {
			if matches(pkg.ImportPath, banned) {
				problems = append(problems, fmt.Sprintf("netctl depends on %s: netctl must not do I/O or touch the OS", pkg.ImportPath))
			}
		}
		if pkg.Module == nil || !pkg.Module.Main {
			continue
		}
		if pkg.Name == "main" {
			problems = append(problems, fmt.Sprintf("%s is package main: netctl is a library and builds no binary (D18)", pkg.ImportPath))
		}
		for _, imported := range pkg.Imports {
			for _, banned := range bannedDirect {
				if matches(imported, banned) {
					problems = append(problems, fmt.Sprintf("%s imports %s: netctl must not touch the OS", pkg.ImportPath, imported))
				}
			}
		}
	}
	sort.Strings(problems)
	return problems
}

// listPackages runs `go list -deps -json ./...` from the module root.
func listPackages(t *testing.T) []goPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-json", "./...")
	cmd.Dir = "../.."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list failed: %v\n%s", err, stderr.String())
	}
	var packages []goPackage
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg goPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decoding go list output: %v", err)
		}
		packages = append(packages, pkg)
	}
	if len(packages) == 0 {
		t.Fatal("go list returned no packages; the guard would check nothing")
	}
	return packages
}

// TestNetctlStaysPure is P1-ARCH-001: the real module must pass the rules.
func TestNetctlStaysPure(t *testing.T) {
	for _, v := range violations(listPackages(t)) {
		t.Error(v)
	}
}

// TestGuardCatchesViolations proves the rules fire, so a silently broken
// guard cannot pass for a clean module.
func TestGuardCatchesViolations(t *testing.T) {
	inThisModule := &struct{ Main bool }{Main: true}
	cases := []struct {
		name     string
		packages []goPackage
		want     string
	}{
		{"os/exec anywhere", []goPackage{{ImportPath: "os/exec", Standard: true}}, "depends on os/exec"},
		{"net/http subpackage", []goPackage{{ImportPath: "net/http/httputil", Standard: true}}, "depends on net/http/httputil"},
		{"x/sys", []goPackage{{ImportPath: "golang.org/x/sys/unix"}}, "depends on golang.org/x/sys/unix"},
		{"netlink", []goPackage{{ImportPath: "github.com/vishvananda/netlink"}}, "depends on github.com/vishvananda/netlink"},
		{"database/sql", []goPackage{{ImportPath: "database/sql"}}, "depends on database/sql"},
		{"direct syscall", []goPackage{{ImportPath: "github.com/VectorIHub/netctl/cidr", Name: "cidr", Module: inThisModule, Imports: []string{"syscall"}}}, "cidr imports syscall"},
		{"package main", []goPackage{{ImportPath: "github.com/VectorIHub/netctl/tool", Name: "main", Module: inThisModule}}, "is package main"},
	}
	for _, tc := range cases {
		got := violations(tc.packages)
		if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), tc.want) {
			t.Errorf("%s: want a violation containing %q, got %v", tc.name, tc.want, got)
		}
	}

	clean := []goPackage{
		{ImportPath: "syscall", Standard: true}, // reached through os; allowed transitively
		{ImportPath: "github.com/VectorIHub/netctl/cidr", Name: "cidr", Module: inThisModule, Imports: []string{"net/netip", "fmt"}},
		{ImportPath: "gopkg.in/yaml.v3"},
	}
	if got := violations(clean); len(got) != 0 {
		t.Errorf("clean graph reported violations: %v", got)
	}
}
