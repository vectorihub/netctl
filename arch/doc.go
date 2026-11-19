// Package arch holds the architecture guard for netctl (P1-ARCH-001).
//
// It contains only tests. netctl decides and never does: no library package
// may reach code that runs programs, talks to the network, opens a database or
// configures the operating system. The tests here fail the build the moment
// that stops being true. Nothing imports this package.
package arch
