package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	artifactstore "commander/framework/artifact/store"
	"commander/framework/comms"
	"commander/providers/fs"
	fsartifact "commander/providers/fs/artifact"
	fscomms "commander/providers/fs/comms"
)

type dependencies struct {
	Bus           comms.Bus
	ArtifactStore artifactstore.ArtifactStore
	closers       []io.Closer
}

func (d *dependencies) Close() (err error) {
	for i := len(d.closers) - 1; i >= 0; i-- {
		err = errors.Join(err, d.closers[i].Close())
	}
	return err
}

func openDependencies(ctx context.Context, configuration config, agentID string) (result *dependencies, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	storageRoot, err := fs.FilesystemPath(configuration.storageURL)
	if err != nil {
		return nil, fmt.Errorf("storage-url: %w", err)
	}
	busRoot, err := fs.FilesystemPath(configuration.busURL)
	if err != nil {
		return nil, fmt.Errorf("bus-url: %w", err)
	}
	d := &dependencies{}
	defer func() {
		if err != nil {
			err = errors.Join(err, d.Close())
		}
	}()

	storage, err := fsartifact.NewArtifactFsStore(storageRoot)
	if err != nil {
		return nil, fmt.Errorf("storage-url: %w", err)
	}
	d.ArtifactStore, err = storage.ForAgent(agentID)
	if err != nil {
		return nil, fmt.Errorf("storage-url: scope artifact store: %w", err)
	}

	bus, err := fscomms.NewFsBus(busRoot)
	if err != nil {
		return nil, fmt.Errorf("bus-url: %w", err)
	}
	d.Bus = bus
	d.closers = append(d.closers, bus)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d, nil
}
