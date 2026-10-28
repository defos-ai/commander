package fixtures

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
	agentfs "github.com/defos-ai/commander/providers/fs/agents"
)

func OpenAgentSourceStore(t *testing.T, root string) *agentfs.AgentSourceFsStore {
	t.Helper()
	store, err := agentfs.NewAgentSourceFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func AgentSource(number int, trusted bool) agents.AgentSource {
	stamp := time.Date(2026, 10, 28, 10, 0, 0, 123456789, time.UTC)
	entry := agents.AgentSource{
		Name: fmt.Sprintf("fixture-%d", number), Description: "Agent source fixture.",
		Source: fmt.Sprintf("github.com/example/agent-%d@v0.2.0", number), Visibility: packages.Public,
		Trusted: trusted, Version: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if trusted {
		entry.TrustUpdatedBy = "sources-operator"
		entry.TrustUpdatedAt = stamp
	}
	return entry
}

func AgentSourceReplacement(entry agents.AgentSource, trusted bool) agents.AgentSource {
	entry.Version++
	entry.Trusted = trusted
	entry.UpdatedAt = entry.UpdatedAt.Add(time.Nanosecond)
	entry.TrustUpdatedBy = "sources-operator"
	entry.TrustUpdatedAt = entry.UpdatedAt
	return entry
}

func WriteAgentSourceBytes(t *testing.T, root string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "agent-sources.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func ReadAgentSourceBytes(t *testing.T, root string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "agent-sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func InvalidAgentSourceDocuments(t *testing.T) map[string][]byte {
	t.Helper()
	entry := AgentSource(1, true)
	valid, err := json.Marshal([]agents.AgentSource{entry})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := json.Marshal([]agents.AgentSource{entry, entry})
	if err != nil {
		t.Fatal(err)
	}
	entry.Version = 0
	invalid, err := json.Marshal([]agents.AgentSource{entry})
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{
		"null":            []byte("null"),
		"object":          []byte("{}"),
		"invalid JSON":    []byte("["),
		"unknown field":   []byte("[{\"unexpected\":true}]"),
		"trailing value":  append(append([]byte(nil), valid...), []byte(" []")...),
		"duplicate key":   duplicate,
		"invalid version": invalid,
	}
}
