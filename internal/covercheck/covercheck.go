// Package covercheck reads a Go coverage profile and reports coverage against the Phase 1
// gate: 85% overall and 90% for ipam, route, sim and validate. It is a test-support package,
// used by a test, because netctl builds no binary.
package covercheck

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Thresholds are the minimum coverage by package directory; "" is the overall figure.
var Thresholds = map[string]float64{"": 85, "ipam": 90, "route": 90, "sim": 90, "validate": 90}

// Result is the coverage of one package against its threshold.
type Result struct {
	// Package is the directory, or "overall".
	Package string
	// Percent is the share of statements covered.
	Percent float64
	// Need is the threshold.
	Need float64
}

// Ok reports whether the threshold is met.
func (r Result) Ok() bool { return r.Percent >= r.Need }

// String writes the result on one line.
func (r Result) String() string {
	status := "ok"
	if !r.Ok() {
		status = "BELOW"
	}
	return fmt.Sprintf("%-10s %5.1f%% (need %.0f%%) %s", r.Package, r.Percent, r.Need, status)
}

// Check reads a coverage profile and returns one result per threshold, sorted by package.
// A block that appears once for every test binary built with it counts once, as covered if
// any binary covered it.
//
// Example:
//
//	results, err := covercheck.Check(file)
func Check(r io.Reader) ([]Result, error) {
	type block struct {
		pkg   string
		stmts int
		hit   bool
	}
	blocks := map[string]*block{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		sp := strings.LastIndex(line, " ")
		colon := strings.LastIndex(line, ":")
		if sp < 0 || colon < 0 {
			continue
		}
		var stmts, count int
		fields := strings.Fields(line[colon+1:])
		if len(fields) != 3 {
			continue
		}
		fmt.Sscan(fields[1], &stmts)
		fmt.Sscan(fields[2], &count)
		key := line[:sp]
		b := blocks[key]
		if b == nil {
			b = &block{pkg: packageOf(line[:colon]), stmts: stmts}
			blocks[key] = b
		}
		b.hit = b.hit || count > 0
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	total, covered := map[string]int{}, map[string]int{}
	for _, b := range blocks {
		for _, key := range []string{"", b.pkg} {
			total[key] += b.stmts
			if b.hit {
				covered[key] += b.stmts
			}
		}
	}
	var out []Result
	for pkg, need := range Thresholds {
		if total[pkg] == 0 {
			continue
		}
		name := pkg
		if name == "" {
			name = "overall"
		}
		out = append(out, Result{name, 100 * float64(covered[pkg]) / float64(total[pkg]), need})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out, nil
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
