package runtimes

import (
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/crypto"
)

// SecretBootstrap grants the deployment key only to Commander's trusted builder
// package. Manifest types and repository visibility cannot grant this access.
type SecretBootstrap struct {
	key string
}

func NewSecretBootstrap(key string) (*SecretBootstrap, error) {
	if _, err := crypto.NewSecretCipher(key); err != nil {
		return nil, err
	}
	return &SecretBootstrap{key: key}, nil
}

// KeyFor accepts the resolved package pin, never a catalog alias or moving tag.
// A nil bootstrap disables key delivery.
func (b *SecretBootstrap) KeyFor(reference, agentType string) string {
	if b == nil || agentType != "builder-agent" {
		return ""
	}
	ref, err := packages.ParseReference(reference)
	if err != nil || ref.Owner != "defos-ai" || ref.Repository != "commander" || ref.ModulePath != "apps/agents/builder" {
		return ""
	}
	return b.key
}

func (SecretBootstrap) String() string   { return "secret bootstrap (key redacted)" }
func (SecretBootstrap) GoString() string { return "secret bootstrap (key redacted)" }
