package fixtures

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

func readYAML(t *testing.T, root, file string, target any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
}
