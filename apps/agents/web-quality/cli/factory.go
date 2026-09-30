package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"commander/framework/comms"
	"commander/framework/domain/artifacts"
	"commander/providers/fs"
	fsartifact "commander/providers/fs/artifact"
	fscomms "commander/providers/fs/comms"
	s3artifact "commander/providers/s3/artifact"
	s3store "commander/providers/s3/store"
)

type dependencies struct {
	Bus               comms.Bus
	ArtifactFileStore artifacts.ArtifactFileStore
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d, nil
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
		return nil, errors.New("RabbitMQ bus is not implemented")
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
		return fsartifact.NewArtifactFileFsStore(root)
	case "s3":
		storage, err := s3store.Open(ctx, storageURL)
		if err != nil {
			return nil, err
		}
		return s3artifact.NewArtifactFileS3Store(storage)
	default:
		return nil, fmt.Errorf("unsupported artifact file store scheme %q", scheme)
	}
}

func connectionScheme(rawURL string) string {
	if filepath.VolumeName(rawURL) != "" {
		return "file"
	}
	// Parse only the prefix: MongoDB seed lists are not standard URL authorities.
	if prefix, _, ok := strings.Cut(rawURL, ":"); ok {
		if parsed, err := url.Parse(prefix + ":"); err == nil && parsed.Scheme != "" {
			return parsed.Scheme
		}
	}
	return "file"
}
