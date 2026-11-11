//go:build unix

package local

import (
	"os/exec"
	"syscall"
)

func detachProcess(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}
