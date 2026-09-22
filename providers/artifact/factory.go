package artifact

import (
	"context"
	"fmt"

	artifactstore "commander/framework/artifact/store"
	fsartifact "commander/providers/fs/artifact"
	"commander/providers/internal/connections"
)

// StorageOptions scopes artifact access for an agent. An empty AgentID allows
// the controller to retrieve artifacts belonging to any agent.
type StorageOptions struct {
	AgentID string
}

type Storage struct {
	ArtifactStore artifactstore.ArtifactStore
}

// Close releases storage resources after consumers have stopped. Filesystem
// stores hold no persistent connections and need no cleanup.
func (s *Storage) Close() error { return nil }

func OpenStorage(ctx context.Context, rawURL string, options StorageOptions) (*Storage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := connections.FilesystemPath(rawURL)
	if err != nil {
		return nil, err
	}
	artifacts, err := fsartifact.NewArtifactFsStore(root)
	if err != nil {
		return nil, fmt.Errorf("open artifact store: %w", err)
	}
	if options.AgentID != "" {
		artifacts, err = artifacts.ForAgent(options.AgentID)
		if err != nil {
			return nil, fmt.Errorf("scope artifact store: %w", err)
		}
	}
	return &Storage{ArtifactStore: artifacts}, nil
}
