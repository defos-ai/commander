//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/defos-ai/commander/apps/agents/builder"
	"github.com/defos-ai/commander/apps/agents/spawner"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 4 {
		return fmt.Errorf("usage: agent-versions STORAGE BUILDER_VERSION SPAWNER_VERSION")
	}
	files, err := fsartifacts.NewArtifactFileFsStore(os.Args[1])
	if err != nil {
		return err
	}
	built, err := builder.NewAgent(config(files, os.Args[2]), "")
	if err != nil {
		return err
	}
	spawned, err := spawner.NewAgent(config(files, os.Args[3]), nil, nil, "")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var snapshots []agent.AgentSnapshot
	for _, instance := range []*agent.Agent{built, spawned} {
		if err := instance.DiscoverActions(ctx, nil); err != nil {
			return err
		}
		snapshots = append(snapshots, instance.Describe())
	}
	return json.NewEncoder(os.Stdout).Encode(snapshots)
}

func config(files artifacts.ArtifactFileStore, version string) agent.AgentConfig {
	return agent.AgentConfig{
		Version: version, ArtifactFileStore: files, Actions: []action.Executor{},
		Server: agent.ServerInfo{Hostname: "embedded-version-fixture", IPAddress: "127.0.0.1"},
	}
}
