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

func OpenCatalogStore(t *testing.T, root string) *agentfs.CatalogFsStore {
	t.Helper()
	store, err := agentfs.NewCatalogFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func CatalogEntry(number int, trusted bool) agents.CatalogEntry {
	stamp := time.Date(2026, 10, 28, 10, 0, 0, 123456789, time.UTC)
	entry := agents.CatalogEntry{
		Name: fmt.Sprintf("fixture-%d", number), Description: "Catalog package fixture.",
		Package: fmt.Sprintf("github.com/example/agent-%d@v0.2.0", number), Visibility: packages.Public,
		Trusted: trusted, Version: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
	if trusted {
		entry.TrustUpdatedBy = "catalog-operator"
		entry.TrustUpdatedAt = stamp
	}
	return entry
}

func CatalogReplacement(entry agents.CatalogEntry, trusted bool) agents.CatalogEntry {
	entry.Version++
	entry.Trusted = trusted
	entry.UpdatedAt = entry.UpdatedAt.Add(time.Nanosecond)
	entry.TrustUpdatedBy = "catalog-operator"
	entry.TrustUpdatedAt = entry.UpdatedAt
	return entry
}

func WriteCatalogBytes(t *testing.T, root string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "agent-catalog.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func ReadCatalogBytes(t *testing.T, root string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "agent-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func InvalidCatalogDocuments(t *testing.T) map[string][]byte {
	t.Helper()
	entry := CatalogEntry(1, true)
	valid, err := json.Marshal([]agents.CatalogEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := json.Marshal([]agents.CatalogEntry{entry, entry})
	if err != nil {
		t.Fatal(err)
	}
	entry.Version = 0
	invalid, err := json.Marshal([]agents.CatalogEntry{entry})
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
