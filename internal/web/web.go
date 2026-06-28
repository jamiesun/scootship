// Package web carries the dashboard's templates and static assets, embedded into
// the binary so scootship ships as a single file with no separate web process
// and no Node toolchain (roadmap: single-binary, embed-only).
package web

import (
	"embed"
	"html/template"
	"io/fs"
)

//go:embed assets
var assets embed.FS

// Static is the sub-filesystem served at /static/.
func Static() fs.FS {
	sub, err := fs.Sub(assets, "assets/static")
	if err != nil {
		panic(err) // embedded layout is fixed at build time
	}
	return sub
}

// Templates parses the dashboard templates with the given function map.
func Templates(funcs template.FuncMap) (*template.Template, error) {
	return template.New("scootship").Funcs(funcs).ParseFS(assets, "assets/templates/*.html")
}
