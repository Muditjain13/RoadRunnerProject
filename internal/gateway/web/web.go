// Package web embeds the demo frontend into the gateway binary, so
// running gateway is enough to get a working UI at http://localhost:8080
// with no separate web server or build step. It lives in its own package so
// the demo UI stays clearly separate from the backend services.
package web

import "embed"

//go:embed static
var Static embed.FS
