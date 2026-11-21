package validate

import (
	"fmt"
	"strings"
)

// plural writes a count and its noun, adding an s unless the count is one.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Text writes the result for people: a line that says whether the plan stands and how many
// findings there are of each level, then each finding with its hint and its evidence, then
// whatever was suppressed with the reason. The same result always gives the same text.
//
// Example:
//
//	fmt.Print(res.Text())
//	// validation: OK (0 errors, 1 warning, 0 infos)
//
//	warning V-TOPO-004 builder-1~dgx-spark
//	  builder-1 and dgx-spark are both behind NAT: ...
func (r Result) Text() string {
	var b strings.Builder
	status := "OK"
	if !r.OK {
		status = "FAILED"
	}
	fmt.Fprintf(&b, "validation: %s (%s, %s, %s)\n", status, plural(len(r.Errors()), "error"), plural(len(r.Warnings()), "warning"), plural(len(r.Infos()), "info"))
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "\n%s %s %s\n  %s\n", f.Level, f.Rule, f.Subject, f.Message)
		if f.Hint != "" {
			fmt.Fprintf(&b, "  hint: %s\n", f.Hint)
		}
		if f.Detail != "" {
			for _, line := range strings.Split(f.Detail, "\n") {
				fmt.Fprintf(&b, "    | %s\n", line)
			}
		}
	}
	if len(r.Suppressed) > 0 {
		fmt.Fprintf(&b, "\nsuppressed (%d):\n", len(r.Suppressed))
		for _, s := range r.Suppressed {
			fmt.Fprintf(&b, "  %s %s: %s\n", s.Finding.Rule, s.Finding.Subject, s.Reason)
		}
	}
	return b.String()
}

// CatalogueText writes the catalogue of rules for people, one line each: the ID, the level
// and what it checks, sorted by ID. It is what a reader needs to choose a rule to suppress.
//
// Example:
//
//	fmt.Print(validate.CatalogueText())
//	// V-ADDR-001  error    no two allocations overlap
func CatalogueText() string {
	var b strings.Builder
	for _, r := range Rules() {
		fmt.Fprintf(&b, "%-11s %-8s %s\n", r.ID, r.Level, r.Title)
	}
	return b.String()
}
