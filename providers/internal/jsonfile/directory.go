package jsonfile

import (
	"os"
	"path/filepath"
)

// PrepareDirectory confirms the directory hierarchy, including ancestors left
// behind by an interrupted setup, before a manifest can refer to files below it.
func PrepareDirectory(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	for current := path; ; current = filepath.Dir(current) {
		if err := syncDirectory(current); err != nil {
			return err
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}
