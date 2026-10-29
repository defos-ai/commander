//go:build (!unix && !windows) || aix

package state

import (
	"errors"
	"os"
)

var errUnsupported = errors.New("Playground profile locking is unsupported on this operating system")

func openRegular(*os.Root, string, bool) (*os.File, error) { return nil, errUnsupported }
func lockFile(*os.File) error                              { return errUnsupported }
func syncRoot(*os.Root) error                              { return errUnsupported }
