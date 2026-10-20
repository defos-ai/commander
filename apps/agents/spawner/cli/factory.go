package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/native"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/providers/fs"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	rabbitmqcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
	s3artifacts "github.com/defos-ai/commander/providers/s3/artifacts"
	s3store "github.com/defos-ai/commander/providers/s3/store"
)

type dependencies struct {
	Bus               comms.Bus
	ArtifactFileStore artifacts.ArtifactFileStore
	Runtime           runtimes.Runtime
	closers           []func() error
}

func (d *dependencies) Close() (err error) {
	for i := len(d.closers) - 1; i >= 0; i-- {
		err = errors.Join(err, d.closers[i]())
	}
	d.closers = nil
	return err
}

func openDependencies(ctx context.Context, configuration config) (result *dependencies, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := &dependencies{}
	defer func() {
		if err != nil {
			err = errors.Join(err, d.Close())
		}
	}()

	d.ArtifactFileStore, err = createArtifactFileStore(ctx, configuration.storageURL)
	if err != nil {
		return nil, fmt.Errorf("storage-url: %w", err)
	}
	bus, err := createBus(ctx, configuration.busURL)
	if err != nil {
		return nil, fmt.Errorf("bus-url: %w", err)
	}
	d.Bus = bus
	d.closers = append(d.closers, bus.Close)
	if err := d.openRuntime(ctx, configuration); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *dependencies) openRuntime(ctx context.Context, configuration config) (err error) {
	resolver := packages.NewResolver(nil)
	d.closers = append(d.closers, func() error {
		resolver.CloseIdleConnections()
		return nil
	})
	switch configuration.runtime {
	case "native":
		d.Runtime, err = native.NewManager(ctx, native.Config{
			Directory: configuration.stateDir, Bus: d.Bus,
			BusURL: configuration.busURL, StorageURL: configuration.storageURL,
			Environment:     childEnvironment(configuration.storageURL, configuration.runtime),
			SecretBootstrap: configuration.secretBootstrap,
			Resolver:        resolver, RegistrationTimeout: configuration.registrationTimeout,
		})
	case "docker":
		engine, engineErr := docker.NewEngine()
		if engineErr != nil {
			return engineErr
		}
		d.closers = append(d.closers, engine.Close)
		d.Runtime, err = docker.NewManager(ctx, docker.Config{
			Directory: configuration.stateDir, Bus: d.Bus,
			BusURL: configuration.busURL, StorageURL: configuration.storageURL,
			Environment:     childEnvironment(configuration.storageURL, configuration.runtime),
			SecretBootstrap: configuration.secretBootstrap,
			Resolver:        resolver, Engine: engine, Network: configuration.dockerNetwork,
			RegistrationTimeout: configuration.registrationTimeout,
		})
	default:
		return fmt.Errorf("unsupported spawner runtime %q", configuration.runtime)
	}
	if err != nil {
		return err
	}
	// Stop runtime work before closing its clients and shared bus, including
	// when later host construction fails.
	d.closers = append(d.closers, d.Runtime.Close)
	return nil
}

func createBus(ctx context.Context, busURL string) (comms.Bus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch scheme := connectionScheme(busURL); scheme {
	case "file", "fs":
		root, err := fs.FilesystemPath(busURL)
		if err != nil {
			return nil, err
		}
		return fscomms.NewFsBus(root)
	case "amqp", "amqps":
		return rabbitmqcomms.NewRabbitMQBus(ctx, busURL, rabbitmqcomms.Options{})
	default:
		return nil, fmt.Errorf("unsupported bus scheme %q", scheme)
	}
}

func createArtifactFileStore(ctx context.Context, storageURL string) (artifacts.ArtifactFileStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch scheme := connectionScheme(storageURL); scheme {
	case "file", "fs":
		root, err := fs.FilesystemPath(storageURL)
		if err != nil {
			return nil, err
		}
		return fsartifacts.NewArtifactFileFsStore(root)
	case "s3":
		storage, err := s3store.Open(ctx, storageURL)
		if err != nil {
			return nil, err
		}
		return s3artifacts.NewArtifactFileS3Store(storage)
	default:
		return nil, fmt.Errorf("unsupported artifact file store scheme %q", scheme)
	}
}

func connectionScheme(rawURL string) string {
	if filepath.VolumeName(rawURL) != "" {
		return "file"
	}
	if prefix, _, ok := strings.Cut(rawURL, ":"); ok {
		if parsed, err := url.Parse(prefix + ":"); err == nil && parsed.Scheme != "" {
			return parsed.Scheme
		}
	}
	return "file"
}

// Children run in private directories, so relative provider paths must be
// resolved before launch. Checking every scheme here avoids opening half a host
// when another provider setting is unsupported.
func normalizeConnection(value, name string, remoteSchemes ...string) (string, error) {
	scheme := connectionScheme(value)
	if scheme == "file" || scheme == "fs" {
		path, err := fs.FilesystemPath(value)
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		return path, nil
	}
	if slices.Contains(remoteSchemes, scheme) {
		return value, nil
	}
	return "", fmt.Errorf("%s: unsupported scheme %q", name, scheme)
}
