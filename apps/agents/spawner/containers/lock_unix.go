//go:build unix && !aix

package containers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func lockState(directory string) (func() error, error) {
	file, err := os.OpenFile(filepath.Join(directory, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open Docker state lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errors.New("another Docker spawner already uses this state directory")
		}
		return nil, fmt.Errorf("lock Docker state directory: %w", err)
	}
	// Closing the descriptor releases the lock, including after a process crash.
	// Keep the file: removing it would allow another process to lock a new inode.
	return file.Close, nil
}
