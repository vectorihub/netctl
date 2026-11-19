// Package arch holds the architecture guard for netctl (P1-ARCH-001).
//
// This package is empty on purpose. The guard is a test, in arch/tests. netctl
// decides and never does: no library package may reach code that runs programs,
// talks to the network, opens a database or configures the operating system.
// The test fails the build the moment that stops being true. Nothing imports
// this package.
package arch
