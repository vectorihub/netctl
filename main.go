// Command beaver-netctl plans, validates and simulates the Beaver network.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is the release version printed by the "version" command. It is "dev"
// in a plain build and is set at release time:
//
//	go build -ldflags "-X main.version=v0.1.0" .
var version = "dev"

// Exit codes (features/netctl, V77).
const (
	exitOK    = 0
	exitUsage = 1
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one invocation of the command and returns the process exit code.
//
// It takes its output streams as arguments instead of using os.Stdout, so a test
// (or another program) can drive it without starting a subprocess.
//
// Exit codes: 0 on success, 1 for a usage error (no command, or an unknown one).
//
// Example:
//
//	var out, errOut bytes.Buffer
//	code := run([]string{"version"}, &out, &errOut)
//	// code == 0
//	// out.String() == "beaver-netctl dev\n"
//
//	code = run([]string{"bogus"}, &out, &errOut)
//	// code == 1; errOut holds `unknown command "bogus"` and the usage text
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}

	switch args[0] {
	case "version", "--version":
		fmt.Fprintf(stdout, "beaver-netctl %s\n", version)
		return exitOK
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "beaver-netctl: unknown command %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `beaver-netctl plans, validates and simulates the Beaver network.

Usage:
  beaver-netctl <command>

Commands:
  version    print the version
  help       show this help
`)
}
