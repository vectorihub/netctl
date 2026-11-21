package phase1

import (
	"strings"

	"github.com/vectorihub/netctl/internal/e2e"
)

// init registers the harness smoke test.
func init() {
	register("P1-HARNESS-001", func(w *e2e.W) {
		fresh, base := e2e.Baseline(w)
		w.ExpectOracleOK(fresh)
		w.ExpectOracleOK(base)
		w.ExpectIPAMOK(base)
	})
}

// contains reports whether text holds the substring, for failure checks on error text.
func contains(text, sub string) bool { return strings.Contains(text, sub) }
