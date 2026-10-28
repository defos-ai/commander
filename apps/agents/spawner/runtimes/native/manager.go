package native

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/domain/actions"
)

// Config contains trusted host configuration, never action or manifest input.
type Config struct {
	Directory           string
	Bus                 comms.Bus
	BusURL              string
	StorageURL          string
	Environment         map[string]string
	SecretBootstrap     *runtimes.SecretBootstrap
	Resolver            *packages.Resolver
	RegistrationTimeout time.Duration
}

type Manager struct {
	config        Config
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	children      map[string]*child
	closed        bool
	once          sync.Once
	closeErr      error
	ownedResolver bool
}

func NewManager(ctx context.Context, config Config) (*Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.Bus == nil || strings.TrimSpace(config.Directory) == "" {
		return nil, errors.New("native spawner requires a bus and state directory")
	}
	if config.BusURL == "" || config.StorageURL == "" {
		return nil, errors.New("native spawner requires bus and storage bootstrap settings")
	}
	if config.RegistrationTimeout < 0 {
		return nil, errors.New("registration timeout must be positive")
	}
	if config.RegistrationTimeout == 0 {
		config.RegistrationTimeout = 30 * time.Second
	}
	var err error
	config.Environment, err = copyEnvironment(config.Environment)
	if err != nil {
		return nil, err
	}
	config.Directory, err = filepath.Abs(config.Directory)
	if err != nil {
		return nil, fmt.Errorf("spawner state directory: %w", err)
	}
	for _, path := range []string{config.Directory, filepath.Join(config.Directory, "downloads"), filepath.Join(config.Directory, "agents")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, fmt.Errorf("create spawner state: %w", err)
		}
	}
	ownedResolver := config.Resolver == nil
	if ownedResolver {
		config.Resolver = packages.NewResolver(nil)
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{config: config, ctx: ctx, cancel: cancel, children: make(map[string]*child), ownedResolver: ownedResolver}
	go func() {
		<-ctx.Done()
		_ = m.Close()
	}()
	return m, nil
}

// Close terminates and awaits owned children before the caller closes shared
// providers. Execution checkpoints prevent another host reusing their launch IDs.
func (m *Manager) Close() error {
	m.once.Do(func() {
		m.cancel()
		m.mu.Lock()
		m.closed = true
		children := make([]*child, 0, len(m.children))
		for _, child := range m.children {
			children = append(children, child)
		}
		m.mu.Unlock()
		var wg sync.WaitGroup
		failures := make(chan error, len(children))
		for _, child := range children {
			wg.Add(1)
			go func() {
				defer wg.Done()
				failures <- child.stop()
			}()
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			m.closeErr = errors.Join(m.closeErr, err)
		}
		if m.ownedResolver {
			m.config.Resolver.CloseIdleConnections()
		}
	})
	return m.closeErr
}

func cloneResult(result runtimes.Result) runtimes.Result {
	result.Actions = append([]actions.Action{}, result.Actions...)
	for i := range result.Actions {
		result.Actions[i] = result.Actions[i].Clone()
	}
	return result
}
