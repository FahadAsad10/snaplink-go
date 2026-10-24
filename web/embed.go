// Package web holds the static web UI, embedded into the binary.
package web

import "embed"

//go:embed index.html
var FS embed.FS
