package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{"no args prints usage and fails", nil, exitUsage, "", "Usage:"},
		{"version", []string{"version"}, exitOK, "beaver-netctl dev", ""},
		{"help", []string{"help"}, exitOK, "Usage:", ""},
		{"unknown command", []string{"bogus"}, exitUsage, "", `unknown command "bogus"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if got := run(tc.args, &out, &errb); got != tc.wantCode {
				t.Fatalf("exit code = %d, want %d", got, tc.wantCode)
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Errorf("stdout %q lacks %q", out.String(), tc.wantOut)
			}
			if !strings.Contains(errb.String(), tc.wantErr) {
				t.Errorf("stderr %q lacks %q", errb.String(), tc.wantErr)
			}
		})
	}
}

// Example shows what a user sees; it also runs as a test, so it cannot go stale.
func Example() {
	run([]string{"version"}, os.Stdout, os.Stderr)
	// Output: beaver-netctl dev
}
