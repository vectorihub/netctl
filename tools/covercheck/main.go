// Command covercheck reads a Go coverage profile and fails when coverage is below the Phase 1
// gate: 85% overall and 90% for ipam, route, sim and validate. It is a development tool, not
// part of the library, and nothing imports it.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// thresholds are the minimum coverage by package directory; "" is the overall figure.
var thresholds = map[string]float64{"": 85, "ipam": 90, "route": 90, "sim": 90, "validate": 90}

// main reads the profile named on the command line and prints the coverage of each package
// with a threshold, exiting 1 if any is short.
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: covercheck cover.out")
		os.Exit(2)
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer f.Close()
	total, covered := map[string]int{}, map[string]int{}
	// The same block appears once for every test binary that was built with it; count it once,
	// as covered if any binary covered it.
	type block struct {
		pkg   string
		stmts int
		hit   bool
	}
	blocks := map[string]*block{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		var file string
		var stmts, count int
		colon := strings.LastIndex(line, ":")
		if colon < 0 {
			continue
		}
		file = line[:colon]
		if _, err := fmt.Sscanf(line[colon+1:], "%*s %d %d", &stmts, &count); err != nil {
			rest := strings.Fields(line[colon+1:])
			if len(rest) != 3 {
				continue
			}
			fmt.Sscan(rest[1], &stmts)
			fmt.Sscan(rest[2], &count)
		}
		b := blocks[line[:strings.LastIndex(line, " ")]]
		if b == nil {
			b = &block{pkg: packageOf(file), stmts: stmts}
			blocks[line[:strings.LastIndex(line, " ")]] = b
		}
		b.hit = b.hit || count > 0
	}
	for _, b := range blocks {
		for _, key := range []string{"", b.pkg} {
			total[key] += b.stmts
			if b.hit {
				covered[key] += b.stmts
			}
		}
	}
	failed := false
	for pkg, min := range thresholds {
		if total[pkg] == 0 {
			continue
		}
		pct := 100 * float64(covered[pkg]) / float64(total[pkg])
		name := pkg
		if name == "" {
			name = "overall"
		}
		status := "ok"
		if pct < min {
			status, failed = "BELOW", true
		}
		fmt.Printf("%-10s %5.1f%% (need %.0f%%) %s\n", name, pct, min, status)
	}
	if failed {
		os.Exit(1)
	}
}

// packageOf returns the top-level package directory of a profile path such as
// "github.com/vectorihub/netctl/ipam/allocate.go".
func packageOf(file string) string {
	rest := file
	if i := strings.Index(file, "/netctl/"); i >= 0 {
		rest = file[i+len("/netctl/"):]
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[:i]
	}
	return ""
}
