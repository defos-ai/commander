//go:build !unix

package docker

// Directory Sync is not supported on all native platforms.
func syncDirectory(_ string) error { return nil }
