//go:build (!unix && !windows) || aix

package jsonfile

import (
	"errors"
	"os"
)

var errUnsupported = errors.New("JSON collection file locking is unsupported on this operating system")

func openRootFile(*os.Root, string, bool) (*os.File, error) { return nil, errUnsupported }
func tryLock(*os.File) (bool, error)                        { return false, errUnsupported }
func syncDirectory(string) error                            { return errUnsupported }

func syncRoot(*os.Root) error { return errUnsupported }
