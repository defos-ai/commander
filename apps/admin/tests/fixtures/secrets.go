package fixtures

import (
	"testing"

	"github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/apps/admin/tests/fakes"
)

type SecretAPI struct {
	*fakes.SecretAPI
	connection client.Connection
}

func NewSecretAPI(t testing.TB) *SecretAPI {
	t.Helper()
	api := fakes.NewSecretAPI(t)
	return &SecretAPI{SecretAPI: api, connection: StartAPI(t, api)}
}

func (f *SecretAPI) Connection() client.Connection { return f.connection }
