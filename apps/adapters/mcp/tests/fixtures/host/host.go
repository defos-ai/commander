package host

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
	"github.com/defos-ai/commander/framework/agent/packages"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/host"
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

func Args(root string) []string {
	return []string{
		"--bus-url=" + filepath.Join(root, "bus"),
		"--storage-url=" + filepath.Join(root, "storage"),
		"--database-url=" + filepath.Join(root, "database"),
		"--listen-address=127.0.0.1:0", "--log-level=debug", "--log-format=json",
	}
}

func Start(t *testing.T, credentials *host.BearerCredentials, logs io.Writer) (string, *http.Client, func()) {
	t.Helper()
	return StartWithOptions(t, nil, cli.Options{Credentials: credentials}, logs)
}

func StartWithOptions(t *testing.T, extraArgs []string, options cli.Options, logs io.Writer) (string, *http.Client, func()) {
	t.Helper()
	t.Setenv("ENV", "")
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	args := append(Args(t.TempDir()), extraArgs...)
	options.Ready = func(address net.Addr) { ready <- address }
	go func() {
		done <- cli.RunWithOptions(ctx, args, logs, options)
	}()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("MCP shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("MCP did not stop within five seconds")
		}
	}
	t.Cleanup(stop)
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	t.Cleanup(transport.CloseIdleConnections)
	select {
	case address := <-ready:
		return "http://" + address.String(), client, stop
	case err := <-done:
		stopped = true
		cancel()
		t.Fatalf("MCP stopped before becoming ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("MCP did not become ready within five seconds")
	}
	return "", nil, nil
}

func Session(t *testing.T, config adapter.Config) (context.Context, *sdkmcp.ClientSession) {
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
	config.Operations = operations
	if config.CatalogClient == nil {
		config.CatalogClient = packages.NewClient(nil)
		t.Cleanup(config.CatalogClient.CloseIdleConnections)
	}
	database, storage := filepath.Join(root, "database"), filepath.Join(root, "storage")
	config.AgentStore, err = fsagents.NewAgentFsStore(database)
	check(err)
	if config.MissionStore == nil {
		config.MissionStore, err = fsmissions.NewMissionFsStore(database)
		check(err)
	}
	if config.OperationStore == nil {
		config.OperationStore, err = fsoperations.NewOperationFsStore(database)
		check(err)
	}
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
	session, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "query-test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	check(err)
	t.Cleanup(func() { check(session.Close()) })
	return ctx, session
}
