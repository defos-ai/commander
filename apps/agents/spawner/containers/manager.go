package containers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/framework/comms"
)

// Config is trusted host configuration. Packages and action input cannot supply
// container privileges, networks, mounts, arguments or bootstrap credentials.
type Config struct {
	Directory           string
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
	config       Config
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	observations map[string]*observation
	once         sync.Once
	releaseLock  func() error
	closeErr     error
}

func NewManager(ctx context.Context, config Config) (*Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.Bus == nil || config.Engine == nil || strings.TrimSpace(config.Directory) == "" {
		return nil, errors.New("Docker spawner requires a bus, engine and state directory")
	}
	if err := ValidateNetwork(config.Network); err != nil {
		return nil, err
	}
	bus, busErr := url.Parse(config.BusURL)
	storage, storageErr := url.Parse(config.StorageURL)
	if busErr != nil || storageErr != nil || (bus.Scheme != "amqp" && bus.Scheme != "amqps") || bus.Host == "" || storage.Scheme != "s3" || storage.Host == "" {
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
	var err error
	config.Directory, err = filepath.Abs(config.Directory)
	if err != nil {
		return nil, fmt.Errorf("spawner state directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(config.Directory, "records"), 0700); err != nil {
		return nil, fmt.Errorf("create Docker launch records: %w", err)
	}
	release, err := lockState(config.Directory)
	if err != nil {
		return nil, err
	}
	if config.Resolver == nil {
		config.Resolver = packages.NewResolver(nil)
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{config: config, ctx: ctx, cancel: cancel, observations: make(map[string]*observation), releaseLock: release}
	go func() { <-ctx.Done(); _ = m.Close() }()
	return m, nil
}

// Close ends registration observation. Containers and their launch records
// survive the spawner; the caller owns the Engine and shared providers.
func (m *Manager) Close() error {
	m.once.Do(func() {
		m.cancel()
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, current := range m.observations {
			<-current.done
		}
		m.config.Resolver.CloseIdleConnections()
		if err := m.releaseLock(); err != nil {
			m.closeErr = fmt.Errorf("release Docker state lock: %w", err)
		}
	})
	return m.closeErr
}

func (m *Manager) environment(agentID string) map[string]string {
	values := maps.Clone(m.config.Environment)
	if values == nil {
		values = make(map[string]string)
	}
	values["BUS_URL"] = m.config.BusURL
	values["STORAGE_URL"] = m.config.StorageURL
	values["COMMANDER_AGENT_ID_SUFFIX"] = agentID[strings.LastIndexByte(agentID, '-')+1:]
	return values
}
