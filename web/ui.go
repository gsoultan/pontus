package web

import "embed"

//go:generate bun install
//go:generate bun run build

// Dist contains the built UI files.
//
//go:embed all:dist
var Dist embed.FS
