// Package migrations contains the versioned database schema.
package migrations

import _ "embed"

//go:embed 001_init.sql
var Initial string
