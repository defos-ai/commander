package fixtures

import (
	"testing"

	"github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/framework/host"
)

func NewClient(t testing.TB, endpoint string) *client.Client {
	t.Helper()
	api, err := client.New(client.Connection{URL: endpoint, Credentials: host.BasicCredentials{Username: "operator", Password: "private-password"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	return api
}
