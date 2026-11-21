package phase1

import "github.com/vectorihub/netctl/internal/e2e"

// init registers the harness smoke test.
func init() {
	register("P1-HARNESS-001", func(w *e2e.W) {
		fresh, base := e2e.Baseline(w)
		w.ExpectOracleOK(fresh)
		w.ExpectOracleOK(base)
		w.ExpectIPAMOK(base)
	})
}
