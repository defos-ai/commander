package e2e

import (
	"net/http/httptest"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes/sessionworker"
)

func PartitionableSessionPeer(t *testing.T) (*httptest.Server, *sessionworker.NetworkPeer) {
	t.Helper()
	network := &sessionworker.NetworkPeer{}
	server := httptest.NewServer(network)
	t.Cleanup(server.Close)
	return server, network
}
