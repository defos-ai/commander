//go:build unix && !linux

package native

import "syscall"

// The portable Go process API protects the direct child's identity during
// Wait. Group cleanup needs a non-reaping wait, currently implemented on Linux.
// Do not signal a numeric process group after Wait may have released its PID.
func terminate(c *child) error { return c.command.Process.Signal(syscall.SIGTERM) }
func kill(c *child) error      { return c.command.Process.Kill() }

func waitProcess(c *child) error {
	_ = c.command.Wait()
	return nil
}
