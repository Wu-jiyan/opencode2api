// Package webui embeds the built management console.
package webui

import (
	"embed"
	"io/fs"
)

// assets is the Vite build output. `all:` keeps dotfiles so a future asset
// naming change cannot silently drop files from the binary.
//
//go:embed all:dist
var assets embed.FS

// Assets is the console rooted at the build output directory.
var Assets = mustSub(assets, "dist")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
