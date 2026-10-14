//go:build embed_ui

package static

import (
	"embed"
	"io/fs"
)

// Packaging places the Vite build here before compiling with -tags embed_ui.
// A missing build fails compilation instead of producing an empty Admin UI.
//
//go:embed dist
var frontend embed.FS

func bundledFrontend() (fs.FS, error) { return fs.Sub(frontend, "dist") }
