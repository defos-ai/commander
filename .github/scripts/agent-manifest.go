// Command agent-manifest renders a built-in agent's release manifest from the
// binaries and image digest produced by the same release run.
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/defos-ai/commander/apps/agents/builder"
	"github.com/defos-ai/commander/apps/agents/spawner"
	"github.com/defos-ai/commander/framework/agent/manifest"
)

func main() {
	agent := flag.String("agent", "", "Built-in agent: builder or spawner")
	version := flag.String("version", "", "Released Commander version")
	digest := flag.String("digest", "", "Published image digest from this release run")
	artifacts := flag.String("artifacts", "", "Directory containing this run's native binaries")
	output := flag.String("output", "", "Destination agent.yaml")
	flag.Parse()
	if err := generate(*agent, *version, *digest, *artifacts, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(agent, version, digest, artifacts, output string) error {
	if agent != "builder" && agent != "spawner" {
		return fmt.Errorf("agent must be builder or spawner")
	}
	if output == "" {
		return fmt.Errorf("output path is required")
	}
	definition := manifest.Manifest{
		SchemaVersion: 1,
		Type:          agent + "-agent",
		Version:       version,
		Docker: &manifest.Docker{
			Image: "ghcr.io/defos-ai/commander-" + agent + "-agent@" + digest,
		},
	}
	var descriptions []manifest.Action
	if agent == "builder" {
		definition.Responsibilities = builder.Responsibilities()
		descriptions = builder.ActionDescriptions()
	} else {
		definition.Responsibilities = spawner.Responsibilities()
		descriptions = spawner.ActionDescriptions()
	}
	definition.Actions = append(definition.Actions, descriptions...)
	if err := definition.Validate(); err != nil {
		return err
	}
	if artifacts == "" {
		return fmt.Errorf("native artifacts directory is required")
	}
	assets, err := nativeAssets(agent, artifacts)
	if err != nil {
		return err
	}
	definition.Native = assets
	data, err := manifest.Render(definition)
	if err != nil {
		return err
	}
	return os.WriteFile(output, data, 0644)
}

func nativeAssets(agent, directory string) (map[string]manifest.NativeAsset, error) {
	assets := make(map[string]manifest.NativeAsset, 6)
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			name := "commander-" + agent + "-agent_" + goos + "_" + goarch
			if goos == "windows" {
				name += ".exe"
			}
			checksum, err := fileChecksum(filepath.Join(directory, name))
			if err != nil {
				return nil, fmt.Errorf("native asset %s: %w", name, err)
			}
			assets[goos+"/"+goarch] = manifest.NativeAsset{Asset: name, SHA256: checksum}
		}
	}
	return assets, nil
}

func fileChecksum(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("expected a nonempty regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
