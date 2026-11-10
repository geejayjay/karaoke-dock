// Package webui embeds the built frontend.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// Files returns the built frontend.
func Files() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
