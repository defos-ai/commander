// Package testing selects cleanup implementations for isolated test environments.
package testing

import (
	frameworktesting "commander/framework/testing"
	fstesting "commander/providers/fs/testing"
	"commander/providers/internal/connections"
)

// OpenCleaner uses the same connections as the test hosts. Only filesystem
// connections are implemented; unsupported backends fail without deleting data.
func OpenCleaner(storageURL, databaseURL string) (frameworktesting.TestCleaner, error) {
	paths := make([]string, 0, 2)
	for _, connection := range []string{storageURL, databaseURL} {
		path, err := connections.FilesystemPath(connection)
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return fstesting.NewTestFsCleaner(paths[0], paths[1])
}
