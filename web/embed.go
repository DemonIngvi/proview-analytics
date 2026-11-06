// Package web embeds the admin dashboard (plain ES modules, no build step).
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var static embed.FS

// FS returns the dashboard files rooted at static/.
func FS() fs.FS {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
