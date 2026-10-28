package fixtures

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/agent/manifest"
)

func ReleaseAssets(t *testing.T) ([]map[string]any, []byte) {
	t.Helper()
	value := manifest.Manifest{SchemaVersion: 1, Type: ReleaseAgentType, Version: ReleaseVersion, Native: map[string]manifest.NativeAsset{}, Docker: &manifest.Docker{Image: "ghcr.io/fixture/commander-" + ReleaseAgentType + "@sha256:" + strings.Repeat("a", 64)}}
	var assets []map[string]any
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			platform := goos + "/" + goarch
			name := "commander-" + ReleaseAgentType + "_" + goos + "_" + goarch
			if goos == "windows" {
				name += ".exe"
			}
			content := []byte("native executable for " + platform)
			digest := sha256.Sum256(content)
			hexDigest := hex.EncodeToString(digest[:])
			value.Native[platform] = manifest.NativeAsset{Asset: name, SHA256: hexDigest}
			assets = append(assets, map[string]any{"id": 101 + len(assets), "name": name, "state": "uploaded", "size": len(content), "digest": "sha256:" + hexDigest})
		}
	}
	var data []byte
	var err error
	data, err = manifest.Render(value)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	assets = append(assets, map[string]any{"id": 100, "name": "agent.yaml", "state": "uploaded", "size": len(data), "digest": "sha256:" + hex.EncodeToString(digest[:])})
	return assets, data
}
