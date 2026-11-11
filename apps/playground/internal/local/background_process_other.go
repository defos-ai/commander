//go:build !unix && !windows

package local

import (
	"errors"
	"os/exec"
)

func detachProcess(*exec.Cmd) error {
	return errors.New("background Commander is unsupported on this operating system")
}
