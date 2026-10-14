//go:build !embed_ui

package static

import "io/fs"

func bundledFrontend() (fs.FS, error) { return nil, nil }
