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

	apicli "github.com/defos-ai/commander/apps/adapters/api/cli"
	mcpcli "github.com/defos-ai/commander/apps/adapters/mcp/cli"
	adminclient "github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/apps/admin/missions"
	spawnercli "github.com/defos-ai/commander/apps/agents/spawner/cli"
	controllercli "github.com/defos-ai/commander/apps/controllers/default/cli"
	"github.com/defos-ai/commander/apps/playground/internal/adminhost"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/host"
)

type runningHost struct {
	name     string
	stop     context.CancelFunc
	done     chan struct{}
	err      error
	required bool
}

type Options struct {
	ServeAdmin bool
	Directory  string
}

type Session struct {
	Context        context.Context
	MCPURL         string
	APIURL         string
	AdminURL       string
	APICredentials host.BasicCredentials
	MCPCredentials host.BearerCredentials
	Directory      string
	Notices        []string
	AgentCount     int
	cancel         context.CancelCauseFunc
	hosts          []*runningHost
	log            *os.File
	once           sync.Once
	closeErr       error
	admin          *adminhost.Manager
	persistent     bool
}

func Start(ctx context.Context) (session *Session, err error) {
	return StartWithOptions(ctx, Options{})
}

func StartWithOptions(ctx context.Context, options Options) (session *Session, err error) {
	s := &Session{}
	s.Context, s.cancel = context.WithCancelCause(ctx)
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	cipher, err := s.prepareState(options.Directory)
	if err != nil {
		return nil, err
	}
	s.log, err = os.OpenFile(filepath.Join(s.Directory, "hosts.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
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
		args := append(append([]string{}, common...), database, "--heartbeat-timeout=45s")
		return controllercli.RunWithOptions(ctx, args, s.log, controllercli.Options{SecretCipher: cipher, Ready: func() { close(controllerReady) }})
	})
	if err = awaitReady(startup, controllerReady); err != nil {
		return nil, err
	}
	mcpReady := make(chan struct{})
	s.start("MCP", func(ctx context.Context) error {
		args := append(append([]string{}, common...), database, "--listen-address=127.0.0.1:0")
		return mcpcli.RunWithOptions(ctx, args, s.log, mcpcli.Options{Application: "commander", Credentials: &s.MCPCredentials, Ready: func(address net.Addr) {
			s.MCPURL = "http://" + address.String() + "/mcp"
			close(mcpReady)
		}})
	})
	if err = awaitReady(startup, mcpReady); err != nil {
		return nil, err
	}
	apiReady := make(chan struct{})
	s.start("API", func(ctx context.Context) error {
		address := os.Getenv("API_LISTEN_ADDRESS")
		if address == "" {
			address = "127.0.0.1:4274"
		}
		args := append(append([]string{}, common...), database, "--listen-address="+address)
		return apicli.RunWithOptions(ctx, args, s.log, apicli.Options{Application: "commander", Credentials: &s.APICredentials, SecretCipher: cipher, Ready: func(address net.Addr) {
			s.APIURL = localHTTPURL(address)
			close(apiReady)
		}})
	})
	if err = awaitReady(startup, apiReady); err != nil {
		return nil, err
	}
	api, err := adminclient.New(adminclient.Connection{URL: s.APIURL, Credentials: s.APICredentials})
	if err != nil {
		return nil, err
	}
	defer api.Close()
	if _, err = missions.Seed(startup, api, false); err != nil {
		return nil, fmt.Errorf("seed bundled missions: %w", err)
	}
	spawnerReady := make(chan struct{})
	expected := make(map[string]string)
	s.start("spawner agent", func(ctx context.Context) error {
		args := append([]string{}, common...)
		return spawnercli.RunWithOptions(ctx, args, s.log, spawnercli.Options{
			Runtime: "native", StateDir: filepath.Join(s.Directory, "spawner"),
			Ready: func(agentID string) {
				expected[agentID] = "spawner-agent"
				close(spawnerReady)
			},
		})
	})
	if err = awaitReady(startup, spawnerReady); err != nil {
		return nil, err
	}
	if err = s.awaitAgents(startup, expected); err != nil {
		return nil, err
	}
	s.admin = adminhost.New(s.Context, s.log, s.APIURL)
	if options.ServeAdmin {
		s.AdminURL, err = s.ServeAdmin(startup, nil)
		if err != nil {
			fmt.Fprintf(s.log, "Admin UI startup: %v\n", err)
			s.Notices = append(s.Notices, "Admin UI unavailable; Playground is ready. Use --debug for details.")
		}
	}
	if err = s.Context.Err(); err != nil {
		return nil, context.Cause(s.Context)
	}
	stateNotice := "Temporary session · results deleted on exit"
	if s.persistent {
		stateNotice = "Persistent session · results retained between runs"
	}
	s.Notices = append(s.Notices, "\nTry: Install the web agent, then review defos.ai\n\n"+stateNotice)
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
	s.startHost(name, true, run)
}

func (s *Session) startHost(name string, required bool, run func(context.Context) error) *runningHost {
	// Close stops hosts in reverse order, allowing agents to publish their final
	// results while the controller is still available.
	ctx, cancel := context.WithCancel(context.WithoutCancel(s.Context))
	host := &runningHost{name: name, stop: cancel, done: make(chan struct{}), required: required}
	s.hosts = append(s.hosts, host)
	go func() {
		err := run(ctx)
		if required && ctx.Err() == nil {
			if err == nil {
				err = errors.New("stopped unexpectedly")
			}
			s.cancel(fmt.Errorf("local %s: %w", name, err))
		}
		if !required && err != nil && !cancellationOnly(err) {
			fmt.Fprintf(s.log, "local %s: %v\n", name, err)
		}
		host.err = err
		close(host.done)
	}()
	return host
}

// Wildcard listeners are bind addresses, not URLs for a local browser or client.
func localHTTPURL(address net.Addr) string {
	host, port, _ := net.SplitHostPort(address.String())
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "::1"
		if ip.To4() != nil {
			host = "127.0.0.1"
		}
	}
	return "http://" + net.JoinHostPort(host, port)
}

func (s *Session) awaitAgents(ctx context.Context, expected map[string]string) error {
	client, err := bridge.Connect(ctx, s.MCPURL, s.MCPCredentials)
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
				ID     string `json:"id"`
				Status string `json:"status"`
				Type   string `json:"type"`
				Stale  bool   `json:"stale"`
			} `json:"agents"`
		}
		if err := json.Unmarshal(result.Data, &inventory); err != nil {
			return err
		}
		matched, connected := 0, 0
		for _, agent := range inventory.Agents {
			if agent.Status != "ready" || agent.Stale {
				continue
			}
			connected++
			if expectedType, expectedAgent := expected[agent.ID]; expectedAgent {
				if expectedType == agent.Type {
					matched++
				}
			}
		}
		if matched == len(expected) {
			s.AgentCount = connected
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("local agent startup: %w", context.Cause(ctx))
		case <-tick.C:
		}
	}
}

func (s *Session) Close() error {
	s.once.Do(func() {
		s.cancel(context.Canceled)
		if s.admin != nil {
			s.closeErr = errors.Join(s.closeErr, s.admin.Close())
		}
		retainState := false
		for i := len(s.hosts) - 1; i >= 0; i-- {
			host := s.hosts[i]
			host.stop()
			<-host.done
			retainState = retainState || errors.Is(host.err, spawnercli.ErrShutdown)
			if err := host.err; host.required && err != nil && !cancellationOnly(err) {
				s.closeErr = errors.Join(s.closeErr, fmt.Errorf("stop %s: %w", host.name, err))
			}
		}
		if s.log != nil {
			s.closeErr = errors.Join(s.closeErr, s.log.Close())
		}
		if s.Directory != "" && !s.persistent {
			if retainState {
				s.closeErr = errors.Join(s.closeErr, fmt.Errorf("local state retained at %s because spawner shutdown failed", s.Directory))
			} else {
				s.closeErr = errors.Join(s.closeErr, os.RemoveAll(s.Directory))
			}
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
