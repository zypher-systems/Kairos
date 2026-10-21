// Package migrations embeds the SQL migration files so the server binary
// can apply them at startup without external files.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
