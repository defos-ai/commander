package kubernetes

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/comms"
)

// PodClient is deliberately smaller than the deployment's Kubernetes authority.
// Launch reconciliation never deletes a Pod or recreates a missing instance.
type PodClient interface {
	Create(context.Context, Pod) (Pod, error)
	Get(context.Context, string) (Pod, error)
	ReleaseGate(context.Context, Pod) (Pod, error)
}

type ManagerConfig struct {
	Pod                 Config
	Bus                 comms.Bus
	Client              PodClient
	Resolver            *packages.Resolver
	RegistrationTimeout time.Duration
	PollInterval        time.Duration
}

type Manager struct {
	config        ManagerConfig
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	observations  map[string]*observation
	ownedIntents  map[string]string
	observedUIDs  map[string]string
	instances     map[string]instance
	once          sync.Once
	ownedResolver bool
}

func NewManager(ctx context.Context, config ManagerConfig) (*Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.Bus == nil || config.Client == nil {
		return nil, errors.New("Kubernetes spawner requires a bus and Pod client")
	}
	if err := config.Pod.Validate(); err != nil {
		return nil, err
	}
	if config.RegistrationTimeout < 0 || config.PollInterval < 0 {
		return nil, errors.New("Kubernetes registration timeout and polling interval must be positive")
	}
	if config.RegistrationTimeout == 0 {
		config.RegistrationTimeout = 45 * time.Second
	}
	if config.PollInterval == 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	config.Pod.ImagePullSecrets = slices.Clone(config.Pod.ImagePullSecrets)
	config.Pod.Resources.Requests = maps.Clone(config.Pod.Resources.Requests)
	config.Pod.Resources.Limits = maps.Clone(config.Pod.Resources.Limits)
	ownedResolver := config.Resolver == nil
	if ownedResolver {
		config.Resolver = packages.NewResolver(nil)
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{config: config, ctx: ctx, cancel: cancel,
		observations: make(map[string]*observation), ownedIntents: make(map[string]string),
		observedUIDs: make(map[string]string), instances: make(map[string]instance), ownedResolver: ownedResolver}
	go func() { <-ctx.Done(); _ = m.Close() }()
	return m, nil
}

// Close ends host-owned observation, leaving child Pods and checkpoints intact.
// The caller owns injected provider clients and the Pod client.
func (m *Manager) Close() error {
	m.once.Do(func() {
		m.cancel()
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, current := range m.observations {
			<-current.done
		}
		if m.ownedResolver {
			m.config.Resolver.CloseIdleConnections()
		}
	})
	return nil
}

var _ runtimes.Runtime = (*Manager)(nil)
