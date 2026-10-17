// Package web holds the browser client. Its files are embedded in the binary,
// so a server never loads anything from another origin.
package web

import "embed"

// Static is the client served at the site root.
//
//go:embed static
var Static embed.FS
