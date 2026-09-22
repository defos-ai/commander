// Package local starts an isolated Commander installation for one Playground session.
package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	mcpcli "commander/adapters/mcp/cli"
	agentcli "commander/apps/agents/web-quality/cli"
	controllercli "commander/apps/controllers/default/cli"
	"commander/apps/controllers/default/data"
	bridge "commander/apps/playground/internal/mcp"
)

type runningHost struct {
	name string
	stop context.CancelFunc
	done chan error
}

type Session struct {
	Context   context.Context
	MCPURL    string
	Directory string
	Notices   []string
	cancel    context.CancelCauseFunc
	hosts     []runningHost
	log       *os.File
	once      sync.Once
	closeErr  error
}

// Start reuses the real application hosts. No Go toolchain, checkout, Docker,
// fixed port, or working-directory files are needed by the built executable.
func Start(ctx context.Context) (session *Session, err error) {
	s := &Session{}
	s.Context, s.cancel = context.WithCancelCause(ctx)
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	s.Directory, err = os.MkdirTemp("", "commander-playground-*")
	if err != nil {
		return nil, err
	}
	s.log, err = os.OpenFile(filepath.Join(s.Directory, "hosts.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	missionFile := filepath.Join(s.Directory, "missions.json")
	if err = os.WriteFile(missionFile, data.DefaultMissions, 0600); err != nil {
		return nil, err
	}
	common := []string{
		"--bus-url=" + filepath.Join(s.Directory, "bus"),
		"--storage-url=" + filepath.Join(s.Directory, "storage"),
		"--log-level=info", "--log-format=json",
	}
	database := "--database-url=" + filepath.Join(s.Directory, "database")
	startup, cancelStartup := context.WithTimeout(s.Context, 30*time.Second)
	defer cancelStartup()
	controllerReady := make(chan struct{})
	s.start("controller", func(ctx context.Context) error {
		args := append(append([]string{}, common...), database, "--with-missions="+missionFile, "--heartbeat-timeout=45s")
		return controllercli.RunWithOptions(ctx, args, s.log, controllercli.Options{Ready: func() { close(controllerReady) }})
	})
	if err = awaitReady(startup, controllerReady); err != nil {
		return nil, err
	}
	s.start("agent", func(ctx context.Context) error { return agentcli.Run(ctx, common, s.log) })
	mcpReady := make(chan struct{})
	s.start("MCP", func(ctx context.Context) error {
		args := append(append([]string{}, common...), database, "--listen-address=127.0.0.1:0")
		return mcpcli.RunWithOptions(ctx, args, s.log, mcpcli.Options{Ready: func(address net.Addr) {
			s.MCPURL = "http://" + address.String() + "/mcp"
			close(mcpReady)
		}})
	})
	if err = awaitReady(startup, mcpReady); err != nil {
		return nil, err
	}
	if err = s.awaitAgent(startup); err != nil {
		return nil, err
	}
	s.Notices = []string{
		"All-in-one: 1 MCP, 1 controller, 1 web-quality agent. Hosts and temporary state are removed on exit.",
		"Try: Run review-website for https://defos.ai and summarize what should be fixed before release.",
	}
	return s, nil
}

func awaitReady(ctx context.Context, ready <-chan struct{}) error {
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("local startup: %w", context.Cause(ctx))
	}
}

func (s *Session) start(name string, run func(context.Context) error) {
	// Close stops hosts in reverse order, allowing the agent to publish its final
	// results while the controller is still available.
	ctx, cancel := context.WithCancel(context.WithoutCancel(s.Context))
	host := runningHost{name: name, stop: cancel, done: make(chan error, 1)}
	s.hosts = append(s.hosts, host)
	go func() {
		err := run(ctx)
		if ctx.Err() == nil {
			if err == nil {
				err = errors.New("stopped unexpectedly")
			}
			s.cancel(fmt.Errorf("local %s: %w", name, err))
		}
		host.done <- err
	}()
}

func (s *Session) awaitAgent(ctx context.Context) error {
	client, err := bridge.Connect(ctx, s.MCPURL)
	if err != nil {
		return err
	}
	defer client.Close()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		result, err := client.Call(ctx, "list_agents", json.RawMessage(`{}`))
		if err != nil {
			return err
		}
		if result.IsError {
			return fmt.Errorf("local agent discovery: %s", result.Text)
		}
		var inventory struct {
			Agents []struct {
				Status string `json:"status"`
			} `json:"agents"`
		}
		if err := json.Unmarshal(result.Data, &inventory); err != nil {
			return err
		}
		if len(inventory.Agents) == 1 && inventory.Agents[0].Status == "ready" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("local agent startup: %w", context.Cause(ctx))
		case <-tick.C:
		}
	}
}

// Close waits for every host before removing this session's temporary files.
func (s *Session) Close() error {
	s.once.Do(func() {
		s.cancel(context.Canceled)
		for i := len(s.hosts) - 1; i >= 0; i-- {
			host := s.hosts[i]
			host.stop()
			if err := <-host.done; err != nil && !cancellationOnly(err) {
				s.closeErr = errors.Join(s.closeErr, fmt.Errorf("stop %s: %w", host.name, err))
			}
		}
		if s.log != nil {
			s.closeErr = errors.Join(s.closeErr, s.log.Close())
		}
		if s.Directory != "" {
			s.closeErr = errors.Join(s.closeErr, os.RemoveAll(s.Directory))
		}
	})
	return s.closeErr
}

func cancellationOnly(err error) bool {
	if err == context.Canceled {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !cancellationOnly(child) {
				return false
			}
		}
		return true
	}
	if inner := errors.Unwrap(err); inner != nil {
		return cancellationOnly(inner)
	}
	return false
}
