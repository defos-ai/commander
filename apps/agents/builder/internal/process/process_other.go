//go:build !unix

package process

import "os/exec"

func Configure(command *exec.Cmd) {}
