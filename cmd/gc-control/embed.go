package main

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed web/dist
var embeddedBundle embed.FS

func embeddedWebFS() (fs.FS, error) {
	web, err := fs.Sub(embeddedBundle, "web/dist")
	if err != nil {
		return nil, fmt.Errorf("control center: open embedded web bundle: %w", err)
	}
	return web, nil
}
