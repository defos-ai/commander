package builder_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"path/filepath"
	"testing"
)

func readBuildArchive(t *testing.T, data []byte) map[string]string {
	t.Helper()
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	files := map[string]string{}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg || !filepath.IsLocal(filepath.FromSlash(header.Name)) {
			t.Fatalf("unsafe archive entry %q", header.Name)
		}
		contents, err := io.ReadAll(io.LimitReader(archive, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = string(contents)
	}
	return files
}
