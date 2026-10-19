//go:build !unix

package runtimes

// The selection file is flushed on every platform; directory Sync is not
// supported by every native filesystem.
func syncSelectionDirectory(_ string) error { return nil }
