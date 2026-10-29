package fixtures

import (
	"os"
	"path/filepath"
	"testing"
)

func LocalStateDirectory(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "local")
}

func ReadLocalStateFile(t *testing.T, directory, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatalf("read local state fixture %s: %v", name, err)
	}
	return data
}

func WriteLocalStateFile(t *testing.T, directory, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
		t.Fatalf("write local state fixture %s: %v", name, err)
	}
}

func RemoveLocalStateFile(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(directory, name)); err != nil {
		t.Fatalf("remove local state fixture %s: %v", name, err)
	}
}
