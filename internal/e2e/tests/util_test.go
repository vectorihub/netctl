package tests

import "fmt"

// sprintf formats like fmt.Sprintf; it keeps the fake test's Fatalf short.
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
