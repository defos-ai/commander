package docker

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/framework/comms"
	s3store "github.com/defos-ai/commander/providers/s3/store"
)

// Config is trusted host configuration. Packages and action input cannot supply
// container privileges, networks, mounts, arguments or bootstrap credentials.
type Config struct {
	Bus                 comms.Bus
	BusURL              string
	StorageURL          string
	Environment         map[string]string
	Resolver            *packages.Resolver
	Engine              Engine
	Network             string
	RegistrationTimeout time.Duration
}

type Manager struct {
	config        Config
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	observations  map[string]*observation
	ownedIntents  map[string]string
	once          sync.Once
	ownedResolver bool
}

func NewManager(ctx context.Context, config Config) (*Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.Bus == nil || config.Engine == nil {
		return nil, errors.New("Docker spawner requires a bus and engine")
	}
	if err := ValidateNetwork(config.Network); err != nil {
		return nil, err
	}
	bus, busErr := url.Parse(config.BusURL)
	_, _, storageErr := s3store.ParseURL(config.StorageURL)
	if busErr != nil || storageErr != nil || (bus.Scheme != "amqp" && bus.Scheme != "amqps") || bus.Host == "" {
		return nil, errors.New("Docker agents require a reachable AMQP bus and S3 storage")
	}
	if strings.ContainsRune(config.BusURL+config.StorageURL, 0) {
		return nil, errors.New("invalid Docker bootstrap settings")
	}
	if config.RegistrationTimeout < 0 {
		return nil, errors.New("registration timeout must be positive")
	}
	if config.RegistrationTimeout == 0 {
		config.RegistrationTimeout = 45 * time.Second
	}
	config.Environment = maps.Clone(config.Environment)
	for key, value := range config.Environment {
		switch key {
		case "AWS_ENDPOINT_URL_S3", "AWS_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "S3_USE_PATH_STYLE":
		default:
			return nil, fmt.Errorf("unsupported Docker bootstrap environment setting %q", key)
		}
		if strings.ContainsRune(value, 0) {
			return nil, errors.New("Docker bootstrap environment contains a NUL byte")
		}
	}
	ownedResolver := config.Resolver == nil
	if ownedResolver {
		config.Resolver = packages.NewResolver(nil)
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{config: config, ctx: ctx, cancel: cancel, observations: make(map[string]*observation), ownedIntents: make(map[string]string), ownedResolver: ownedResolver}
	go func() { <-ctx.Done(); _ = m.Close() }()
	return m, nil
}

// Close ends registration observation. Containers survive the spawner;
// the caller owns the Engine and shared providers.
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

func (m *Manager) environment(saved record) map[string]string {
	values := maps.Clone(m.config.Environment)
	if values == nil {
		values = make(map[string]string)
	}
	values["BUS_URL"] = m.config.BusURL
	values["STORAGE_URL"] = m.config.StorageURL
	values["COMMANDER_AGENT_ID_SUFFIX"] = saved.AgentID[strings.LastIndexByte(saved.AgentID, '-')+1:]
	return values
}
