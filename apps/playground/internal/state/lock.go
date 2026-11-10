package state

import (
	"errors"
	"os"
	"strings"
)

var ErrLocked = errors.New("state directory is already in use by another process")

func LockFile(root *os.Root, name string) (*os.File, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, errors.New("state lock requires a file name")
	}
	file, err := openRegular(root, name, true)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if err := file.Chmod(0600); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}
