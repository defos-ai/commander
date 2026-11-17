package host

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentendpoints "github.com/defos-ai/commander/apps/adapters/api/endpoints/agents"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func NewAgentStopAPI(t *testing.T, inventory agents.AgentStore, commands controllercommands.Controller) *API {
	t.Helper()
	mux := http.NewServeMux()
	agentendpoints.Register(mux, inventory)
	agentendpoints.RegisterCommands(mux, commands)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 2 * time.Second
	t.Cleanup(client.CloseIdleConnections)
	return &API{URL: server.URL, Client: client}
}

func JSONBody(t *testing.T, input any) func(*http.Request) {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return func(request *http.Request) {
		request.Body = io.NopCloser(bytes.NewReader(data))
		request.ContentLength = int64(len(data))
		request.Header.Set("Content-Type", "application/json")
	}
}
