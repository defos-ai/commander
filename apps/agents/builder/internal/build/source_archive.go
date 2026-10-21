package build

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
)

// Keep source attachments bounded and use Root for every read. Never follow a
// generated symlink into host data or include Git history and tool credentials.
func ArchiveSource(ctx context.Context, source string, key []byte) ([]byte, int, error) {
	const maxBytes = 16 << 20
	root, err := os.OpenRoot(source)
	if err != nil {
		return nil, 0, errors.New("source directory is unavailable")
	}
	defer root.Close()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	var total int64
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return errors.New("cannot read generated source")
		}
		if name == "." {
			return nil
		}
		if bytes.Contains([]byte(name), key) {
			return errors.New("source filename contains the build API key; remove it before exporting")
		}
		if ExcludedSource(entry.Name()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("source archive cannot contain symlinks or special files")
		}
		file, err := root.Open(name)
		if err != nil {
			return errors.New("cannot open generated source")
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			file.Close()
			return errors.New("source archive cannot contain symlinks or special files")
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxBytes-total+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return errors.New("cannot read generated source")
		}
		total += int64(len(data))
		count++
		if total > maxBytes || count > 1000 {
			return errors.New("source archive exceeds 1000 files or 16 MiB")
		}
		if bytes.Contains(data, key) {
			return errors.New("source contains the build API key; remove it before exporting")
		}
		mode := int64(0644)
		if info.Mode()&0111 != 0 {
			mode = 0755
		}
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tarWriter.Write(data)
		return err
	})
	if err == nil {
		err = tarWriter.Close()
	}
	if err == nil {
		err = gzipWriter.Close()
	}
	if err != nil {
		return nil, 0, err
	}
	if count == 0 {
		return nil, 0, errors.New("generated source is empty")
	}
	return buffer.Bytes(), count, nil
}

func ExcludedSource(name string) bool {
	switch name {
	case ".git", ".codex", ".cache", ".home", "state", "node_modules", "bin", "dist", "auth.json":
		return true
	}
	return name == ".env" || strings.HasPrefix(name, ".env.") && name != ".env.example" || strings.HasPrefix(name, ".build-")
}
