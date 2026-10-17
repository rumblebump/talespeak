// Package content embeds the story packs that ship with the engine.
package content

import (
	"embed"
	"io/fs"
)

//go:embed demo
var embedded embed.FS

// Demo is the small pack the engine runs until packs can be installed.
func Demo() fs.FS {
	sub, err := fs.Sub(embedded, "demo")
	if err != nil {
		panic(err) // the folder is embedded at build time
	}
	return sub
}
