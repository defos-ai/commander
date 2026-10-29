package fixtures

import (
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/crypto"
	secretservice "github.com/defos-ai/commander/framework/secrets"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/secrets"
)

func NewService(t *testing.T) (*fakes.ServiceStore, *crypto.SecretCipher, *secretservice.Service) {
	t.Helper()
	cipher, err := crypto.NewSecretCipher(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	store := fakes.NewServiceStore()
	service, err := secretservice.NewService(store, cipher)
	if err != nil {
		t.Fatal(err)
	}
	return store, cipher, service
}
