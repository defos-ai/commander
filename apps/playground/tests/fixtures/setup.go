package fixtures

import (
	"net/http/httptest"
	"testing"

	"github.com/defos-ai/commander/apps/playground/tests/fakes"
)

func StartSetupMCPProbe(t *testing.T, localToken, explicitToken string) (*fakes.SetupMCPProbe, string) {
	t.Helper()
	probe := fakes.NewSetupMCPProbe(localToken, explicitToken)
	server := httptest.NewServer(probe)
	t.Cleanup(server.Close)
	return probe, server.URL + "/mcp"
}
