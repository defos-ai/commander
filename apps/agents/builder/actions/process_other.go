//go:build !unix

package actions

import "os/exec"

func configureCommand(command *exec.Cmd) {}
