//go:build unix && !aix

package jsonfile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openRootFile(root *os.Root, name string, create bool) (*os.File, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if create {
		flags = unix.O_RDWR | unix.O_CREAT | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	}
	// openRegular accepts one local filename, so no parent traversal can occur
	// between this directory handle and the final no-follow open.
	fd, err := unix.Openat(int(directory.Fd()), name, flags, 0600)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	// NewFile wraps the validated descriptor; name labels errors, not a path lookup.
	return os.NewFile(uintptr(fd), name), nil
}

func tryLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
		return false, nil
	}
	return err == nil, err
}

func syncDirectory(path string) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	return syncRoot(root)
}

func syncRoot(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
