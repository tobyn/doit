// Package stdlib provides the embedded doit standard library file system.
package stdlib

import "embed"

//go:embed *.doit
var FS embed.FS
