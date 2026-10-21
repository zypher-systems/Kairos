package webdist

import "embed"

// FS embeds the built SPA (web/dist output). The tree keeps a placeholder so
// the embed works before the first frontend build.
//
//go:embed all:dist
var FS embed.FS
