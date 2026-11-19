// Package webstatic holds the stylesheet, script and icon shared by the
// public server and the local dashboard, so both look the same and the
// client does not need the server package.
package webstatic

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// FS serves the files at its root (app.css, app.js, icon.svg).
func FS() fs.FS {
	sub, _ := fs.Sub(files, "static")
	return sub
}
