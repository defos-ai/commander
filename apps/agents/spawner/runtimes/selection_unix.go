//go:build unix

package runtimes

import (
	"errors"
	"os"
)

func syncSelectionDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
