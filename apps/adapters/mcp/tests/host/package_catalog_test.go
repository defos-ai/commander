package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/framework/agent/packages"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/id"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	fsmissions "github.com/defos-ai/commander/providers/fs/missions"
	fsoperations "github.com/defos-ai/commander/providers/fs/operations"
	fsreports "github.com/defos-ai/commander/providers/fs/reports"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type packageMetadataTransport func(*http.Request) (*http.Response, error)

func (transport packageMetadataTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestMCPPackageDiscovery(t *testing.T) {
	const source = "https://catalog.example/agents.yaml"
	const catalog = `schema_version: 1
agents:
  zeta:
    description: Private package fixture.
    package: github.com/private-org/private-agent@v1.2.3
    visibility: private
  alpha:
    description: Public package fixture.
    package: github.com/acme/public-agent@stable
    visibility: public
`
	var requests atomic.Int32
	var catalogUnavailable atomic.Bool
	client := packages.NewClient(packageMetadataTransport(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		for _, header := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
			if request.Header.Get(header) != "" {
				t.Errorf("public metadata request included %s", header)
			}
		}
		body, status := "", http.StatusOK
		switch request.URL.String() {
		case source:
			body = catalog
			if catalogUnavailable.Load() {
				status = http.StatusServiceUnavailable
			}
		case "https://api.github.com/repos/other-team/agents/releases/latest":
			body = `{"tag_name":"v2.4.0","draft":false,"prerelease":false}`
		default:
			t.Errorf("unexpected metadata URL: %s", request.URL)
			return nil, errors.New("unexpected metadata request")
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	t.Cleanup(client.CloseIdleConnections)
	ctx, session := packageCatalogSession(t, client, source)
	if requests.Load() != 0 {
		t.Fatal("MCP startup or initialization fetched remote package metadata")
	}
	call := func(name string, args map[string]any, output any) {
		t.Helper()
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("%s did not return package metadata", name)
		}
		content, ok := result.Content[0].(*sdkmcp.TextContent)
		if !ok {
			t.Fatalf("%s did not return JSON text", name)
		}
		if err := json.Unmarshal([]byte(content.Text), output); err != nil {
			t.Fatal(err)
		}
	}
	var listing struct {
		Packages []struct {
			Name, Description, Package, Visibility string
		} `json:"packages"`
	}
	call("list_agent_packages", map[string]any{}, &listing)
	if len(listing.Packages) != 2 || listing.Packages[0].Name != "alpha" || listing.Packages[1].Name != "zeta" ||
		listing.Packages[0].Description != "Public package fixture." || listing.Packages[0].Package != "github.com/acme/public-agent@stable" || listing.Packages[0].Visibility != "public" ||
		listing.Packages[1].Description != "Private package fixture." || listing.Packages[1].Package != "github.com/private-org/private-agent@v1.2.3" || listing.Packages[1].Visibility != "private" {
		t.Fatalf("package listing lost sorted catalog entries or visibility: %+v", listing)
	}
	for _, test := range []struct{ selector, visibility, pinned string }{
		{"github.com/other-team/agents/apps/agents/custom@stable", "public", "github.com/other-team/agents/apps/agents/custom@v2.4.0"},
		{"github.com/elsewhere/custom-agent@v4.5.6", "public", "github.com/elsewhere/custom-agent@v4.5.6"},
		{"github.com/private-org/private-agent@v1.2.3", "private", "github.com/private-org/private-agent@v1.2.3"},
	} {
		var result struct{ Package, Visibility string }
		call("resolve_agent_package", map[string]any{"package": test.selector, "visibility": test.visibility}, &result)
		if result.Package != test.pinned || result.Visibility != test.visibility {
			t.Fatalf("resolution lost the pinned reference or visibility: %+v", result)
		}
	}
	if requests.Load() != 2 {
		t.Fatal("resolution fetched the catalog or contacted GitHub for an exact reference")
	}
	for _, args := range []map[string]any{
		{"package": "github.com/private-org/private-agent@stable", "visibility": "private"},
		{"package": "github.com/acme/public-agent@v1.2.3"},
		{"package": "github.com/acme/public-agent@v1.2.3", "visibility": ""},
		{"package": "github.com/acme/public-agent@v1.2.3", "visibility": nil},
		{"package": "github.com/acme/public-agent@v1.2.3", "visibility": "internal"},
		{"package": "alpha", "visibility": "public"},
	} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "resolve_agent_package", Arguments: args})
		if err == nil && !result.IsError {
			t.Fatalf("invalid package selector or visibility was accepted: %+v", args)
		}
		if args["visibility"] == "private" {
			if err != nil || len(result.Content) != 1 {
				t.Fatal("private stable selection did not return an actionable tool error")
			}
			content, ok := result.Content[0].(*sdkmcp.TextContent)
			if !ok || !strings.Contains(content.Text, "pinned package version") {
				t.Fatal("private stable selection did not request an exact version")
			}
		}
	}
	if requests.Load() != 2 {
		t.Fatal("invalid or private stable selection reached anonymous metadata transport")
	}
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "list_agent_packages", Arguments: map[string]any{"url": "https://other.example/agents.yaml"}})
	if err == nil && !result.IsError {
		t.Fatal("tool input overrode the deployment's catalog URL")
	}
	if requests.Load() != 2 {
		t.Fatal("untrusted catalog URL input reached metadata transport")
	}
	catalogUnavailable.Store(true)
	result, err = session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "list_agent_packages", Arguments: map[string]any{}})
	if err != nil || !result.IsError {
		t.Fatal("unavailable catalog must return a tool error")
	}
	var exact struct{ Package, Visibility string }
	call("resolve_agent_package", map[string]any{"package": "github.com/elsewhere/custom-agent@v4.5.6", "visibility": "public"}, &exact)
	if exact.Package != "github.com/elsewhere/custom-agent@v4.5.6" || requests.Load() != 3 {
		t.Fatal("catalog failure prevented an unrelated exact package resolution")
	}
}

func packageCatalogSession(t *testing.T, client *packages.Client, source string) (context.Context, *sdkmcp.ClientSession) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	check(err)
	t.Cleanup(func() { check(bus.Close()) })
	adapterID, err := id.AdapterID("mcp")
	check(err)
	operations, err := controllercommands.NewClient(ctx, bus, adapterID, nil)
	check(err)
	t.Cleanup(func() { check(operations.Close()) })
	config := adapter.Config{Operations: operations, PackageClient: client, CatalogURL: source}
	database, storage := filepath.Join(root, "database"), filepath.Join(root, "storage")
	config.AgentStore, err = fsagents.NewAgentFsStore(database)
	check(err)
	config.MissionStore, err = fsmissions.NewMissionFsStore(database)
	check(err)
	config.OperationStore, err = fsoperations.NewOperationFsStore(database)
	check(err)
	config.ActionStore, err = fsactions.NewActionFsStore(database)
	check(err)
	config.ArtifactStore, err = fsartifacts.NewArtifactFsStore(database)
	check(err)
	config.ArtifactFileStore, err = fsartifacts.NewArtifactFileFsStore(storage)
	check(err)
	config.ReportStore, err = fsreports.NewReportFsStore(storage)
	check(err)
	server, err := adapter.NewServer(config)
	check(err)
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	check(err)
	t.Cleanup(func() { check(serverSession.Close()) })
	session, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "package-catalog-test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	check(err)
	t.Cleanup(func() { check(session.Close()) })
	return ctx, session
}
