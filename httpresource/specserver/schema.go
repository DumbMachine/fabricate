package specserver

import _ "embed"

//go:embed schema.sql
var schemaSQL string

// SchemaSQL is the generic document table used by spec-driven resources.
func SchemaSQL() string { return schemaSQL }
