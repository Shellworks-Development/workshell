// Package workshell embeds the files the server ships with: the built web
// dashboard and the Luau SDK sources (served as a downloadable Roblox model).
package workshell

import (
	"embed"
	"io/fs"
)

//go:embed all:web/dist
var webDist embed.FS

//go:embed sdk/src
var sdkSrc embed.FS

// WebFS returns the built dashboard (populated by `pnpm build` in web/).
func WebFS() fs.FS {
	sub, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// SDKFS returns the Luau SDK sources (sdk/src).
func SDKFS() fs.FS {
	sub, err := fs.Sub(sdkSrc, "sdk/src")
	if err != nil {
		panic(err)
	}
	return sub
}
