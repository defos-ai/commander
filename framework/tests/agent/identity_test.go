package agents_test

import (
	"encoding/json"
	"testing"

	"commander/framework/agent"
	"commander/framework/id"
	artifactproviders "commander/providers/artifact"
)

func TestAgentIdentity(t *testing.T) {
	// Arrange.
	storage, err := artifactproviders.OpenStorage(t.Context(), t.TempDir(), artifactproviders.StorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	for _, test := range []struct {
		name, kind, identity string
		invalid              bool
	}{
		{name: "default type"},
		{name: "private type", kind: "private-audit"},
		{name: "supplied identity", kind: "private-audit", identity: "private-audit-agent-0123abcd"},
		{name: "mismatched type", kind: "private-audit", identity: "generic-agent-0123abcd", invalid: true},
		{name: "wrong role", kind: "private-audit", identity: "private-audit-controller-0123abcd", invalid: true},
		{name: "legacy identity", identity: "00000000-0000-4000-8000-000000000001", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			config := agent.AgentConfig{ID: test.identity, Type: agent.AgentType(test.kind), ArtifactStore: storage.ArtifactStore}

			// Act.
			instance, err := agent.NewAgent(config)

			// Assert.
			if test.invalid {
				if err == nil {
					t.Fatal("invalid runtime identity was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			kind := test.kind
			if kind == "" {
				kind = "generic"
			}
			if actual, ok := id.RuntimeType(instance.ID(), id.Agent); !ok || actual != kind {
				t.Fatalf("unexpected runtime identity: %q", instance.ID())
			}
			if test.identity != "" && instance.ID() != test.identity {
				t.Fatalf("runtime changed supplied identity: %q", instance.ID())
			}
			description := instance.Describe()
			if description.ID != instance.ID() || string(description.Type) != kind {
				t.Fatalf("description does not preserve runtime identity: %+v", description)
			}
			data, err := json.Marshal(description)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if _, exists := fields["name"]; exists {
				t.Fatal("description still exposes a separate instance name")
			}
		})
	}
}
