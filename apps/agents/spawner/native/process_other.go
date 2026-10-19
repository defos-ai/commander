//go:build !unix

package native

import "os/exec"

func configureCommand(_ *exec.Cmd) {}
func terminate(c *child) error     { return c.command.Process.Kill() }
func kill(c *child) error          { return c.command.Process.Kill() }

func waitProcess(c *child) error {
	_ = c.command.Wait()
	return nil
}

// Directory fsync is not available on every platform. The create-only record
// itself is flushed before starting the process on all platforms.
func syncDirectory(_ string) error { return nil }
