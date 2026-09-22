//go:build !unix

package environment

import "os/exec"

func configureCommand(command *exec.Cmd) {}
