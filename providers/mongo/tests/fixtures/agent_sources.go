package fixtures

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/providers/mongo/store"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func AgentSource(name, reference string) agents.AgentSource {
	stamp := time.Date(2026, 10, 28, 12, 0, 0, 123456789, time.UTC)
	return agents.AgentSource{
		Name: name, Description: "Agent source fixture.", Source: reference,
		Visibility: packages.Public, Version: 1, CreatedAt: stamp, UpdatedAt: stamp,
	}
}

func AgentSourceReplacement(entry agents.AgentSource, trusted bool) agents.AgentSource {
	entry.Version++
	entry.Trusted = trusted
	entry.UpdatedAt = entry.UpdatedAt.Add(time.Nanosecond)
	entry.TrustUpdatedBy = "sources-operator"
	entry.TrustUpdatedAt = entry.UpdatedAt
	return entry
}

func CorruptAgentSourceDocument(t *testing.T, database *store.MongoStore, entry agents.AgentSource, mode string) {
	t.Helper()
	version := strconv.FormatUint(entry.Version, 10)
	if mode == "identity" {
		entry.Source = "github.com/defos-ai/other-agent@v0.3.0"
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	document := bson.M{"version": version, "data": json.RawMessage(data)}
	switch mode {
	case "version":
		document["version"] = "2"
	case "extra field":
		document["trusted"] = true
	case "payload field":
		document["data"] = json.RawMessage(append(data[:len(data)-1], []byte(`,"extra":true}`)...))
	}
	if _, err := database.Update(t.Context(), "agent_sources", bson.D{}, bson.M{"$set": document}); err != nil {
		t.Fatal(err)
	}
}
