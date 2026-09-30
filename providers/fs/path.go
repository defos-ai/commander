package fs

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// FilesystemPath accepts a local path, ~/path, or a file:/// or fs:/// URI.
// URI paths must be absolute, with no remote host, credentials, query or fragment.
// The returned absolute path is not created by this function.
func FilesystemPath(connection string) (string, error) {
	if strings.TrimSpace(connection) == "" {
		return "", errors.New("connection must not be blank")
	}
	path := connection
	if filepath.VolumeName(connection) == "" && strings.Contains(connection, ":") {
		u, err := url.Parse(connection)
		if err != nil {
			return "", errors.New("invalid storage connection URI")
		}
		if u.Scheme != "" {
			if u.Scheme != "file" && u.Scheme != "fs" {
				return "", fmt.Errorf("unsupported storage provider %q; use a filesystem path, file:///path, or fs:///path", u.Scheme)
			}
			if u.User != nil || (u.Host != "" && u.Host != "localhost") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
				return "", errors.New("filesystem URI must have a local absolute path without credentials, query or fragment")
			}
			path = u.Path
			if !filepath.IsAbs(path) {
				return "", errors.New("filesystem URI path must be absolute")
			}
		}
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(homeDir, strings.TrimPrefix(path, "~"))
	} else if strings.HasPrefix(path, "~") {
		return "", errors.New("only the current user's ~/ home directory syntax is supported")
	}
	if strings.ContainsRune(path, 0) {
		return "", errors.New("filesystem path must not contain a NUL byte")
	}
	return filepath.Abs(path)
}
