package adminhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sync"
	"time"

	admincli "github.com/defos-ai/commander/apps/admin/cli"
)

type server struct {
	stop  context.CancelFunc
	done  chan struct{}
	ready chan net.Addr
	err   error
	url   string
}

type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc
	output io.Writer
	apiURL string
	start  sync.Mutex
	mu     sync.Mutex
	server *server
	closed bool
}

func New(ctx context.Context, output io.Writer, apiURL string) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	if output == nil {
		output = io.Discard
	}
	return &Manager{ctx: ctx, cancel: cancel, output: output, apiURL: apiURL}
}

// Start waits for readiness; its context limits startup, not the server lifetime.
func (m *Manager) Start(ctx context.Context, args []string) (string, error) {
	m.start.Lock()
	defer m.start.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	if m.closed || m.ctx.Err() != nil {
		m.mu.Unlock()
		return "", context.Canceled
	}
	if running := m.server; running != nil {
		select {
		case <-running.done:
			running.stop()
			m.server = nil
		default:
			address := running.url
			m.mu.Unlock()
			return address, nil
		}
	}
	serverContext, stop := context.WithCancel(m.ctx)
	running := &server{stop: stop, done: make(chan struct{}), ready: make(chan net.Addr, 1)}
	m.server = running
	m.mu.Unlock()
	go func() {
		running.err = admincli.RunWithOptions(serverContext, m.arguments(args), nil, m.output, m.output, admincli.Options{
			Ready: func(address net.Addr) { running.ready <- address },
		})
		if running.err != nil && serverContext.Err() == nil {
			fmt.Fprintf(m.output, "Admin UI: %v\n", running.err)
		}
		close(running.done)
	}()
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var err error
	select {
	case address := <-running.ready:
		m.mu.Lock()
		if !m.closed && m.ctx.Err() == nil && startup.Err() == nil {
			running.url = localURL(address)
			address := running.url
			m.mu.Unlock()
			return address, nil
		}
		m.mu.Unlock()
		err = startup.Err()
		if err == nil {
			err = context.Canceled
		}
	case <-running.done:
		err = running.err
		if err == nil {
			err = errors.New("Admin UI stopped before becoming ready")
		}
	case <-startup.Done():
		err = startup.Err()
	case <-m.ctx.Done():
		err = context.Canceled
	}
	stop()
	<-running.done
	m.mu.Lock()
	m.server = nil
	m.mu.Unlock()
	return "", err
}

func (m *Manager) arguments(args []string) []string {
	address := os.Getenv("UI_LISTEN_ADDRESS")
	if address == "" {
		address = "127.0.0.1:4275"
	}
	result := []string{"serve", "--listen-address=" + address}
	if os.Getenv("UI_API_UPSTREAMS") == "" && m.apiURL != "" {
		if api, err := url.Parse(m.apiURL); err == nil && api.Host != "" {
			// A local session may use an ephemeral API port; authorize only that host.
			result = append(result, "--api-upstreams="+api.Host+"="+api.Host)
		}
	}
	return append(result, args...)
}

func (m *Manager) URL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil || m.server == nil {
		return ""
	}
	select {
	case <-m.server.done:
		return ""
	default:
		return m.server.url
	}
}

// Close also cancels an in-progress startup before waiting for its listener.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.start.Lock()
	defer m.start.Unlock()
	m.mu.Lock()
	running := m.server
	m.server = nil
	m.mu.Unlock()
	if running != nil {
		running.stop()
		<-running.done
	}
	return nil
}

func localURL(address net.Addr) string {
	host, port, _ := net.SplitHostPort(address.String())
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "::1"
		if ip.To4() != nil {
			host = "127.0.0.1"
		}
	}
	return "http://" + net.JoinHostPort(host, port)
}
