package local

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func detachProcess(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	return nil
}
