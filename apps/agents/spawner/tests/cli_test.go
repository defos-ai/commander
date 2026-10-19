package packages_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/cli"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

func TestSpawnerCLIRegistersCatalogAndStopsStartupChildren(t *testing.T) {
	seenFreshIDs := map[string]bool{}
	for _, test := range []struct {
		name, suffix string
		empty, unset bool
	}{
		{name: "default catalog and two startup agents", suffix: "12345678"},
		{name: "explicit empty catalog", suffix: "1234abcd", empty: true},
		{name: "empty suffix creates a fresh ID", empty: true},
		{name: "unset suffix creates a fresh ID", empty: true, unset: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("COMMANDER_AGENT_ID_SUFFIX", test.suffix)
			if test.unset {
				if err := os.Unsetenv("COMMANDER_AGENT_ID_SUFFIX"); err != nil {
					t.Fatal(err)
				}
			}
			root := t.TempDir()
			bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
			if err != nil {
				t.Fatal(err)
			}
			defer bus.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			subscription, err := bus.Subscribe(ctx, comms.Inbox{Topic: messages.ControllerEvents(messages.DefaultControllerType), Name: "cli-fixture", Mode: comms.TransientExclusive})
			if err != nil {
				t.Fatal(err)
			}
			defer subscription.Close()
			args := []string{
				"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"),
				"--state-dir=" + filepath.Join(root, "spawner"), "--heartbeat-interval=0", "--search-paths=",
				"--registration-timeout=5s",
			}
			resolver, _ := nativeFixtureResolver(t, "")
			if test.empty {
				args = append(args, "--actions=")
			} else {
				args = append(args, "--spawn-agents="+fixtureReference, "--spawn-agents="+fixtureReference)
			}
			serving, stop := context.WithCancel(ctx)
			finished := make(chan struct{})
			var runErr error
			go func() {
				defer close(finished)
				runErr = cli.RunWithOptions(serving, args, io.Discard, cli.Options{Resolver: resolver})
			}()
			defer func() {
				stop()
				select {
				case <-finished:
				case <-time.After(7 * time.Second):
					t.Error("spawner host did not stop")
				}
			}()
			expectedID := ""
			if test.suffix != "" {
				expectedID = "spawner-agent-" + test.suffix
			}
			snapshot := readAgentAnnouncement(t, ctx, subscription, messages.AgentStarted, expectedID)
			if !id.ValidAgent(snapshot.ID) || !strings.HasPrefix(snapshot.ID, "spawner-agent-") {
				t.Fatalf("unexpected spawner identity: %s", snapshot.ID)
			}
			if test.suffix == "" {
				if seenFreshIDs[snapshot.ID] {
					t.Fatalf("fresh host reused agent ID %s", snapshot.ID)
				}
				seenFreshIDs[snapshot.ID] = true
			}
			if snapshot.Type != "spawner-agent" || snapshot.Status != agent.AgentReady || snapshot.DiscoveryScope == nil || snapshot.DiscoveryScope.Paths == nil || len(snapshot.DiscoveryScope.Paths) != 0 {
				t.Fatalf("incorrect host registration: %+v", snapshot)
			}
			if test.empty {
				if len(snapshot.Actions) != 0 {
					t.Fatal("explicit empty catalog was ignored")
				}
			} else {
				if len(snapshot.Actions) != 1 || snapshot.Actions[0].Name != "spawn-agent" || snapshot.Actions[0].Status != actionmodel.Ready {
					t.Fatalf("unexpected action catalog: %+v", snapshot.Actions)
				}
				if reports := childReports(t, filepath.Join(root, "storage")); len(reports) != 2 || reports[0].AgentID == reports[1].AgentID {
					t.Fatalf("repeat startup flags did not produce independent children: %+v", reports)
				}
			}
			stop()
			select {
			case <-finished:
				if !errors.Is(runErr, context.Canceled) {
					t.Fatalf("unexpected shutdown: %v", runErr)
				}
			case <-ctx.Done():
				t.Fatal("spawner shutdown timed out")
			}
			stopped := readAgentAnnouncement(t, ctx, subscription, messages.AgentStopped, snapshot.ID)
			if stopped.Status != agent.AgentStopped {
				t.Fatalf("wrong final status: %s", stopped.Status)
			}
			if runtime.GOOS != "windows" {
				for _, report := range childReports(t, filepath.Join(root, "storage")) {
					if _, err := os.Stat(filepath.Join(root, "storage", report.AgentID+".stopped")); err != nil {
						t.Fatalf("host returned before child cleanup: %v", err)
					}
				}
			}
		})
	}
}

func TestSpawnerCLIValidatesBeforeCreatingState(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "")
	t.Setenv("SPAWNER_RUNTIME", "")
	t.Setenv("SPAWNER_DOCKER_NETWORK", "")
	for _, test := range []struct{ name, argument, expected string }{
		{"empty state", "--state-dir=", "state-dir"},
		{"unknown action", "--actions=checkout-template", "unknown action"},
		{"duplicate action", "--actions=spawn-agent,spawn-agent", "duplicate"},
		{"negative heartbeat", "--heartbeat-interval=-1s", "heartbeat-interval"},
		{"zero registration timeout", "--registration-timeout=0", "registration-timeout"},
		{"negative registration timeout", "--registration-timeout=-1s", "registration-timeout"},
		{"unpinned package", "--spawn-agents=github.com/defos-ai/fixture-agent@latest", "stable version"},
		{"invalid bus", "--bus-url=https://example.invalid", "bus"},
		{"invalid storage", "--storage-url=https://example.invalid", "storage"},
		{"unknown runtime", "--runtime=containerd", "runtime"},
		{"docker without network", "--runtime=docker", "docker-network"},
		{"native with docker network", "--docker-network=commander_default", "docker-network"},
		{"native with empty docker network flag", "--docker-network=", "docker-network"},
		{"positional argument", "unexpected", "positional"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "unused")
			args := []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"), "--state-dir=" + filepath.Join(root, "spawner"), test.argument}
			err := cli.Run(t.Context(), args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("invalid configuration returned %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid configuration created state")
			}
		})
	}
	for _, value := range []string{"builder-agent-12345678", "spawner-agent-12345678", "arbitrary-text", "1234567", "123456789", "1234ABCD", "1234567g", " 12345678", "12345678 ", " "} {
		t.Run("agent id suffix "+value, func(t *testing.T) {
			t.Setenv("COMMANDER_AGENT_ID_SUFFIX", value)
			root := filepath.Join(t.TempDir(), "unused")
			err := cli.Run(t.Context(), []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"), "--state-dir=" + filepath.Join(root, "spawner")}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "COMMANDER_AGENT_ID_SUFFIX") {
				t.Fatalf("invalid ID suffix returned %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid identity created state")
			}
		})
	}
}

func TestSpawnerCLIDockerRequiresRemoteProvidersBeforeStartup(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "")
	t.Setenv("SPAWNER_RUNTIME", "docker")
	t.Setenv("SPAWNER_DOCKER_NETWORK", "commander_default")
	for _, test := range []struct {
		name, busURL, storageURL, expected string
	}{
		{"filesystem bus", "./state/bus", "s3://artifacts", "amqp or amqps"},
		{"filesystem storage", "amqp://example.invalid/", "./state/storage", "s3 storage-url"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "unused")
			err := cli.Run(t.Context(), []string{
				"--state-dir=" + root, "--bus-url=" + test.busURL, "--storage-url=" + test.storageURL,
			}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("invalid Docker providers returned %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid Docker settings created state")
			}
		})
	}
	// Flags override the runtime from the environment, after clearing the
	// Docker network. A canceled native host must never contact Docker.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	t.Setenv("SPAWNER_DOCKER_NETWORK", "")
	if err := cli.Run(ctx, []string{"--runtime=native"}, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("runtime flag did not override environment: %v", err)
	}
}

func TestSpawnerCLIDockerValidatesNetworkAndExplicitTimeoutBeforeProviders(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "")
	t.Setenv("SPAWNER_RUNTIME", "docker")
	t.Setenv("SPAWNER_DOCKER_NETWORK", "commander_default")
	for _, network := range []string{"host", "none", "bridge", "default", "bad/network", "commander default", "../network", strings.Repeat("n", 129)} {
		t.Run(network, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "unused")
			err := cli.Run(t.Context(), []string{
				"--docker-network=" + network, "--state-dir=" + filepath.Join(root, "spawner"),
				"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"),
			}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "docker-network") {
				t.Fatalf("invalid Docker network reached providers: %v", err)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid Docker network created provider state")
			}
		})
	}
	// The Docker default must not overwrite an explicit timeout. Zero and
	// negative durations remain invalid, while a positive override is accepted.
	for _, timeout := range []string{"0s", "-1s", "5s"} {
		t.Run("timeout "+timeout, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			err := cli.Run(ctx, []string{"--bus-url=amqp://example.invalid/", "--storage-url=s3://artifacts", "--registration-timeout=" + timeout}, io.Discard)
			if timeout == "5s" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("valid explicit timeout was rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "registration-timeout") {
				t.Fatalf("invalid explicit timeout was overwritten by default: %v", err)
			}
		})
	}
}

func TestSpawnerCLIHelpAndPreCanceledStartupDoNotTouchProviders(t *testing.T) {
	t.Setenv("BUS_URL", "amqp://user:private-bus-sentinel@example.invalid")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "private-storage-sentinel")
	t.Setenv("SECRETS_ENCRYPTION_KEY", "private-encryption-sentinel")
	root := filepath.Join(t.TempDir(), "unused")
	args := []string{"--bus-url=" + filepath.Join(root, "bus"), "--storage-url=" + filepath.Join(root, "storage"), "--state-dir=" + filepath.Join(root, "spawner")}
	var output bytes.Buffer
	if err := cli.Run(t.Context(), append(args, "--help"), &output); err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"spawn-agent", "spawn-agents", "state-dir", "registration-timeout", "runtime", "docker-network"} {
		if !strings.Contains(output.String(), word) {
			t.Errorf("help omits %s", word)
		}
	}
	if strings.Contains(output.String(), "sentinel") {
		t.Fatal("help exposed environment values")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cli.Run(ctx, args, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled host returned %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("help or pre-canceled startup created state")
	}
}
