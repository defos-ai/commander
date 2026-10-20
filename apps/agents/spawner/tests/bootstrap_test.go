package packages_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/crypto"
)

func TestSecretBootstrapGrantsOnlyPinnedCommanderBuilder(t *testing.T) {
	key := strings.Repeat("ab", 32)
	bootstrap, err := runtimes.NewSecretBootstrap(key)
	if err != nil {
		t.Fatal(err)
	}
	const builder = "github.com/defos-ai/commander/apps/agents/builder@v0.2.0"
	for _, test := range []struct {
		name, reference, agentType string
		allowed                    bool
	}{
		{"builder", builder, "builder-agent", true},
		{"different release", strings.Replace(builder, "v0.2.0", "v0.3.0", 1), "builder-agent", true},
		{"spoofed type", "github.com/other/commander/apps/agents/builder@v0.2.0", "builder-agent", false},
		{"different repository", "github.com/defos-ai/other/apps/agents/builder@v0.2.0", "builder-agent", false},
		{"different module", "github.com/defos-ai/commander/apps/agents/spawner@v0.2.0", "builder-agent", false},
		{"nested module", "github.com/defos-ai/commander/apps/agents/builder/other@v0.2.0", "builder-agent", false},
		{"wrong type", builder, "web-agent", false},
		{"moving selector", strings.Replace(builder, "v0.2.0", "stable", 1), "builder-agent", false},
		{"unresolved URL", "https://" + builder, "builder-agent", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := bootstrap.KeyFor(test.reference, test.agentType)
			if test.allowed && value != key || !test.allowed && value != "" {
				t.Fatal("unexpected bootstrap key access")
			}
		})
	}
	var disabled *runtimes.SecretBootstrap
	if disabled.KeyFor(builder, "builder-agent") != "" || new(runtimes.SecretBootstrap).KeyFor(builder, "builder-agent") != "" {
		t.Fatal("unconfigured bootstrap supplied a key")
	}
	for _, value := range []any{bootstrap, *bootstrap} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			if strings.Contains(fmt.Sprintf(format, value), key) {
				t.Fatal("bootstrap diagnostic formatting disclosed its key")
			}
		}
		data, err := json.Marshal(value)
		if err != nil || strings.Contains(string(data), key) {
			t.Fatal("bootstrap serialization disclosed its key")
		}
	}
	for _, value := range []string{"", "private-invalid-bootstrap-key"} {
		_, err := runtimes.NewSecretBootstrap(value)
		if !errors.Is(err, crypto.ErrMissingKey) && !errors.Is(err, crypto.ErrInvalidKey) {
			t.Fatal("invalid bootstrap key was accepted")
		}
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatal("bootstrap validation disclosed its key")
		}
	}
}
