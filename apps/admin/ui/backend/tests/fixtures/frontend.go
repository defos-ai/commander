package fixtures

import (
	"os"
	"path/filepath"
	"testing"
)

func FrontendFiles() map[string]string {
	return map[string]string{
		"index.html":                 "<html>Commander</html>",
		"assets/app-abcd1234.js":     "console.log('Commander')",
		"assets/app-abcd1234.css":    "body{color:black}",
		"assets/font-abcd1234.woff2": "font fixture",
	}
}

func BuiltFrontend(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range FrontendFiles() {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}
