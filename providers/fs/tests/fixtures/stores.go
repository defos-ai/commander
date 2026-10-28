package fixtures

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"testing"
)

func DenyDirectoryRead(t *testing.T, dir string) func() {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions and an unprivileged user")
	}
	restore := func() {
		t.Helper()
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(restore)
	if err := os.Chmod(dir, 0300); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(dir)
	if err == nil {
		file.Close()
		t.Skip("directory read permissions are not enforced")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("directory permission probe failed unexpectedly: %v", err)
	}
	return restore
}
