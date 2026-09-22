package connections_test

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"commander/providers/internal/connections"
)

func TestFilesystemConnections(t *testing.T) {
	root := t.TempDir()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	withSpace := filepath.Join(root, "my state")
	for _, test := range []struct{ connection, want string }{
		{root, root},
		{"./relative-state", "relative-state"},
		{"~/state", filepath.Join(homeDir, "state")},
		{"~", homeDir},
		{(&url.URL{Scheme: "file", Path: withSpace}).String(), withSpace},
		{(&url.URL{Scheme: "fs", Path: root}).String(), root},
		{(&url.URL{Scheme: "file", Host: "localhost", Path: root}).String(), root},
	} {
		t.Run(test.connection, func(t *testing.T) {
			// Arrange.
			want, err := filepath.Abs(test.want)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			got, err := connections.FilesystemPath(test.connection)

			// Assert.
			if err != nil || got != want {
				t.Fatalf("got %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestInvalidConnectionsAreRejectedWithoutSideEffects(t *testing.T) {
	for _, connection := range []string{
		"", " ", "redis://user:secret@localhost/0", "s3://bucket/path", "postgres:database",
		"file://remote/state", "fs://user:secret@localhost/state", "file:relative",
		"file:///state?mode=rw", "file:///state?", "file:///state#fragment", "file:///%zz",
		"~someone/state", "file:///state%00", "state\x00",
	} {
		t.Run(connection, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			t.Chdir(root)

			// Act.
			path, err := connections.FilesystemPath(connection)

			// Assert.
			if err == nil || path != "" {
				t.Fatalf("invalid connection resolved to %q: %v", path, err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("resolution created filesystem entries: %v, %v", entries, err)
			}
		})
	}
}

func TestResolvingFilesystemPathDoesNotCreateIt(t *testing.T) {
	// Arrange.
	path := filepath.Join(t.TempDir(), "does-not-exist")

	// Act.
	got, err := connections.FilesystemPath(path)

	// Assert.
	if err != nil || got != path {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("resolving a connection should not create it: %v", err)
	}
}
