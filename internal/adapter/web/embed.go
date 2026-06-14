package web

import "embed"

// distFS holds the statically-exported Next.js frontend. The `make web` target
// builds the frontend into ./dist before `go build` embeds it. When the
// frontend hasn't been built, dist contains only .gitkeep and the server
// serves a "not built" placeholder (see frontendFS).
//
//go:embed all:dist
var distFS embed.FS
