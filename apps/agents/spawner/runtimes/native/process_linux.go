//go:build linux

package native

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func terminate(c *child) error { return c.signalGroup(unix.SIGTERM) }
func kill(c *child) error      { return c.signalGroup(unix.SIGKILL) }

func (c *child) signalGroup(signal unix.Signal) error {
	c.processMu.Lock()
	defer c.processMu.Unlock()
	if c.reaped {
		return os.ErrProcessDone
	}
	err := unix.Kill(-c.command.Process.Pid, signal)
	if errors.Is(err, unix.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func waitProcess(c *child) error {
	pid := c.command.Process.Pid
	var information unix.Siginfo
	var err error
	for {
		err = unix.Waitid(unix.P_PID, pid, &information, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	c.processMu.Lock()
	defer c.processMu.Unlock()
	defer func() {
		_ = c.command.Wait()
		c.reaped = true
	}()
	if err != nil {
		// Without an unreaped leader, numeric group identity is uncertain.
		// The direct process handle remains the only safe termination target.
		if !errors.Is(err, unix.ECHILD) {
			_ = c.command.Process.Kill()
		}
		return fmt.Errorf("observe agent %s exit: %w; retain shared state", c.result.AgentID, err)
	}
	// WNOWAIT keeps the leader's PID reserved while its remaining descendants
	// are killed, including when the leader exited before shutdown was requested.
	if err := unix.Kill(-pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("stop agent %s process group: %w; retain shared state", c.result.AgentID, err)
	}
	if err := waitForProcessGroup(pid); err != nil {
		return fmt.Errorf("stop agent %s descendants: %w; retain shared state", c.result.AgentID, err)
	}
	return nil
}

func waitForProcessGroup(group int) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		active, err := groupHasLiveMembers(group)
		if err != nil || !active {
			return err
		}
		if !time.Now().Before(deadline) {
			return errors.New("processes remain after forced termination")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func groupHasLiveMembers(group int) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == group {
			continue
		}
		processGroup, err := unix.Getpgid(pid)
		if errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			return false, err
		}
		if processGroup != group {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		// The parenthesized command may contain spaces or parentheses. The
		// following fields begin with state, parent PID and process-group ID.
		end := strings.LastIndexByte(string(data), ')')
		if end < 0 {
			return false, errors.New("cannot inspect process-group member")
		}
		fields := strings.Fields(string(data[end+1:]))
		if len(fields) < 3 {
			return false, errors.New("cannot inspect process-group member")
		}
		if fields[2] == strconv.Itoa(group) && fields[0] != "Z" && fields[0] != "X" {
			return true, nil
		}
	}
	return false, nil
}
