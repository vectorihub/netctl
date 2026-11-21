package config

import _ "embed"

// schemaJSON is the JSON Schema of a NodeConfig, compiled into the package.
//
//go:embed schema/v1alpha1.json
var schemaJSON []byte

// SchemaJSON returns the JSON Schema of a NodeConfig, the same bytes as the file
// config/schema/v1alpha1.json, for a consumer that wants to validate a config
// without the Go types. The slice is a copy; changing it changes nothing.
//
// Example:
//
//	schema := config.SchemaJSON() // hand it to any JSON Schema validator
func SchemaJSON() []byte { return append([]byte(nil), schemaJSON...) }
