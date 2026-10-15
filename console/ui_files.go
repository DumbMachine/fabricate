package console

import (
	"embed"
	"io/fs"
)

//go:embed all:ui
var uiFiles embed.FS

func uiSub() fs.FS {
	sub, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		panic(err)
	}
	return sub
}
