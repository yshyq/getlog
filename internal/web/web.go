package web

import (
	"embed"
	"io/fs"
)

// static contains the small browser client shipped with the portal binary.
//
//go:embed static/*
var static embed.FS

func Assets() fs.FS {
	assets, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	return assets
}
