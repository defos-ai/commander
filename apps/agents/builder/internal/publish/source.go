package publish

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/defos-ai/commander/apps/agents/builder/internal/build"
)

const MaxArchiveBytes = 20 << 20

// ExtractSource accepts regular source files only, never Git history, credentials
// or archive links. Root keeps every write inside the fresh publication snapshot.
func ExtractSource(ctx context.Context, archive []byte, directory string, token []byte) error {
	if len(archive) > MaxArchiveBytes {
		return errors.New("source archive exceeds 20 MiB")
	}
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return errors.New("source artifact must be a gzip tar archive")
	}
	defer compressed.Close()
	limited := &io.LimitedReader{R: compressed, N: 18<<20 + 1}
	reader := tar.NewReader(limited)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return errors.New("cannot open publication directory")
	}
	defer root.Close()
	var total int64
	files := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("cannot read source archive")
		}
		name := header.Name
		if !utf8.ValidString(name) || name == "." || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") || name == ".." || strings.HasPrefix(name, "../") {
			return errors.New("source archive contains an unsafe path")
		}
		for _, component := range strings.Split(name, "/") {
			if build.ExcludedSource(component) {
				return errors.New("source archive contains Git history, credentials or runtime files")
			}
		}
		if bytes.Contains([]byte(name), token) {
			return errors.New("source filename contains the GitHub token")
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > 16<<20-total || files >= 1000 {
			return errors.New("source archive requires at most 1000 regular files and 16 MiB of source")
		}
		data, err := io.ReadAll(reader)
		if err != nil || int64(len(data)) != header.Size {
			return errors.New("cannot read source archive file")
		}
		if bytes.Contains(data, token) {
			return errors.New("source contains the GitHub token")
		}
		if parent := path.Dir(name); parent != "." {
			if err := root.MkdirAll(parent, 0700); err != nil {
				return errors.New("cannot prepare source directory")
			}
		}
		mode := os.FileMode(0600)
		if header.Mode&0111 != 0 {
			mode = 0700
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return errors.New("source archive contains duplicate or invalid files")
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return errors.New("cannot write publication source")
		}
		total += header.Size
		files++
	}
	if _, err := io.Copy(io.Discard, limited); err != nil || limited.N <= 0 || files == 0 {
		return errors.New("source archive is empty, corrupt or exceeds its decompression limit")
	}
	return addAttribution(root)
}

func addAttribution(root *os.Root) error {
	const footer = "Built with [Commander](https://github.com/defos-ai/commander)."
	readme, err := root.ReadFile("README.md")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot read publication README")
	}
	if !utf8.Valid(readme) {
		return errors.New("publication README must contain UTF-8 text")
	}
	if bytes.Contains(readme, []byte(footer)) {
		return nil
	}
	readme = append(bytes.TrimRight(readme, "\r\n"), []byte("\n\n"+footer+"\n")...)
	if err := root.WriteFile("README.md", readme, 0600); err != nil {
		return errors.New("cannot prepare publication README")
	}
	return nil
}
