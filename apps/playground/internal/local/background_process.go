package local

import (
	"os"
	"os/exec"
)

func startDetached(executable string, args []string, log *os.File) (*exec.Cmd, error) {
	command := exec.Command(executable, args...)
	command.Stdout, command.Stderr = log, log
	if err := detachProcess(command); err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	return command, nil
}
