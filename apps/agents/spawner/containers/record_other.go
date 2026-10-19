//go:build !unix

package containers

// Directory Sync is not supported on all native platforms.
func syncDirectory(_ string) error { return nil }
