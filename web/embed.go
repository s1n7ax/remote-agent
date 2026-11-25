// Package web embeds the built single-page app. Run `npm run build` in this
// directory to populate dist; until then only dist/.gitkeep is embedded.
package web

import "embed"

// Dist holds the built SPA (web/dist), served by internal/api.
//
//go:embed all:dist
var Dist embed.FS
