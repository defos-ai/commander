package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/crypto"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
)

func nativeDetails(t *testing.T, result runtimes.Result) runtimes.NativeRuntimeDetails {
	t.Helper()
	details, ok := result.RuntimeDetails.(runtimes.NativeRuntimeDetails)
	if result.Runtime != runtimes.RuntimeNative || !ok {
		t.Fatalf("expected native runtime details: %+v", result)
	}
	return details
}

func TestMain(m *testing.M) {
	code := m.Run()
	fixtures.CleanupNativeBuild()
	os.Exit(code)
}

func TestNativeSpawnUsesAssignedIdentityAndReusesOnlyItsLiveChild(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "12345678")
	for _, key := range []string{"SPAWNER_PARENT_SECRET", "SECRETS_ENCRYPTION_KEY", "API_PASSWORD", "MCP_TOKEN"} {
		t.Setenv(key, "private-parent-sentinel")
	}
	fixture := fixtures.NewNativeManager(t, "", 5*time.Second)
	firstCtx, firstRequest, firstJournal := fixtures.SpawnCheckpoints(t)

	first, err := fixture.Manager.Spawn(firstCtx, firstRequest, fixtures.Reference)

	if err != nil {
		t.Fatal(err)
	}
	if first.AgentType != "fixture-agent" || !strings.HasPrefix(first.AgentID, "fixture-agent-") || !id.ValidAgent(first.AgentID) || first.Package != fixtures.Reference || nativeDetails(t, first).PID <= 0 {
		t.Fatalf("unexpected spawned identity: %+v", first)
	}
	if len(first.Actions) != 1 || first.Actions[0].Name != "fixture-action" || first.Actions[0].Status != actionmodel.Ready {
		t.Fatalf("registration action inventory was not returned: %+v", first.Actions)
	}
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)

	second, err := fixture.Manager.Spawn(ctx, requestID, fixtures.ModuleReferenceFixture)

	if err != nil {
		t.Fatal(err)
	}
	if second.AgentID == first.AgentID || nativeDetails(t, second).PID == nativeDetails(t, first).PID || second.Package != fixtures.ModuleReferenceFixture {
		t.Fatal("independent requests reused an agent identity or process")
	}
	before := fixture.RequestCount()

	repeated, err := fixture.Manager.Spawn(ctx, requestID, fixtures.ModuleReferenceFixture)

	if err != nil {
		t.Fatal(err)
	}
	if repeated.AgentID != second.AgentID || nativeDetails(t, repeated).PID != nativeDetails(t, second).PID || fixture.RequestCount() != before {
		t.Fatal("same request did not reuse the existing child without downloading again")
	}
	if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
		t.Fatal("request identity accepted a conflicting package")
	}
	reports := fixtures.ChildReports(t, fixture.Storage)
	if len(reports) != 2 {
		t.Fatalf("started %d children; want two", len(reports))
	}
	for _, report := range reports {
		if len(report.AgentIDSuffix) != 8 || report.AgentID != "fixture-agent-"+report.AgentIDSuffix || !id.ValidAgent(report.AgentID) {
			t.Fatalf("child did not construct its assigned identity from a hex suffix: %+v", report)
		}
		if report.InheritedSecret || report.SecretChallenge != nil || report.Region != "fixture-region" || report.Home == "" {
			t.Fatalf("unexpected child environment: %+v", report)
		}
		if report.ChromiumPath != fixture.Config.Environment["CHROMIUM_PATH"] || report.ChromiumNoSandbox != "true" {
			t.Fatalf("child did not receive browser deployment settings: %+v", report)
		}
		if relative, err := filepath.Rel(fixture.Config.Directory, report.Home); err != nil || !filepath.IsLocal(relative) {
			t.Fatalf("child home is not isolated within its spawner directory: %s", report.Home)
		}
	}
	assertCheckpointExcludes(t, firstJournal, "private-parent-sentinel", "fixture-region")
	assertCheckpointExcludes(t, journal, "private-parent-sentinel", "fixture-region")
	if err := fixture.Manager.Close(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, report := range reports {
			if _, err := os.Stat(filepath.Join(fixture.Storage, report.AgentID+".stopped")); err != nil {
				t.Fatalf("Close returned before child shutdown finished: %v", err)
			}
		}
	}
}

func TestNativeBootstrapSharesUsableKeyOnlyWithTrustedBuilder(t *testing.T) {
	key := strings.Repeat("6a", 32)
	bootstrap, err := runtimes.NewSecretBootstrap(key)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SECRETS_ENCRYPTION_KEY", "unrelated-parent-key")
	t.Setenv("API_PASSWORD", "private-parent-sentinel")
	for _, test := range []struct {
		name, mode, reference string
		receivesKey           bool
	}{
		{"trusted builder", "-builder", fixtures.BuilderReference, true},
		{"untrusted builder type", "-builder", fixtures.Reference, false},
		{"wrong manifest type", "", fixtures.BuilderReference, false},
		{"ordinary agent", "", fixtures.Reference, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := fixtures.NewNativeManager(t, test.mode, 5*time.Second, bootstrap)
			ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if _, err := fixture.Manager.Spawn(ctx, requestID, test.reference); err != nil {
				t.Fatal(err)
			}
			report := fixtures.AwaitChildReport(t, ctx, fixture.Storage)
			if report.InheritedSecret {
				t.Fatal("child inherited unrelated host credentials")
			}
			if (report.SecretChallenge != nil) != test.receivesKey {
				t.Fatal("secret key bootstrap did not respect the trusted package and type")
			}
			if report.SecretChallenge != nil {
				plaintext, err := cipher.Decrypt(*report.SecretChallenge)
				defer clear(plaintext)
				if err != nil || string(plaintext) != "fixture secret bootstrap" {
					t.Fatal("child could not encrypt with the deployment's key")
				}
			}
			assertCheckpointExcludes(t, journal, key)
		})
	}
}

func TestNativeRejectsInvalidRegistrationsWithoutRelaunching(t *testing.T) {
	for _, mode := range []string{"-wrong-type", "-wrong-id", "-invalid-schema"} {
		t.Run(mode, func(t *testing.T) {
			fixture := fixtures.NewNativeManager(t, mode, time.Second)
			ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
				t.Fatal("registration with an incorrect identity, type, or action schema was accepted")
			}
			before := fixture.RequestCount()
			if _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
				t.Fatal("retry accepted a mismatched registration")
			}
			if fixture.RequestCount() != before || len(fixtures.ChildReports(t, fixture.Storage)) != 1 {
				t.Fatal("timed-out request started another process")
			}
		})
	}
}

func TestNativeCanceledWaitRetainsOneOwnedLaunch(t *testing.T) {
	fixture := fixtures.NewNativeManager(t, "-silent", 5*time.Second)
	executionCtx, requestID, _ := fixtures.SpawnCheckpoints(t)
	ctx, cancel := context.WithCancel(executionCtx)
	finished := make(chan error, 1)
	go func() { _, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference); finished <- err }()
	defer cancel()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		paths, err := filepath.Glob(filepath.Join(fixture.Storage, "*.started"))
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) == 1 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("fixture child did not start")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled wait returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled spawn did not return")
	}
	before := fixture.RequestCount()
	retryCtx, retryCancel := context.WithTimeout(executionCtx, 200*time.Millisecond)
	defer retryCancel()
	if _, err := fixture.Manager.Spawn(retryCtx, requestID, fixtures.Reference); err == nil {
		t.Fatal("silent child unexpectedly registered")
	}
	if fixture.RequestCount() != before || len(fixtures.ChildReports(t, fixture.Storage)) != 1 {
		t.Fatal("retry after cancellation launched a duplicate")
	}
}

func TestNativeRetryReceivesRegistrationAnnouncedAfterItsInitialWaitEnded(t *testing.T) {
	for _, canceled := range []bool{true, false} {
		name, timeout := "canceled", 5*time.Second
		if !canceled {
			name, timeout = "timed out", 100*time.Millisecond
		}
		t.Run(name, func(t *testing.T) {
			fixture := fixtures.NewNativeManager(t, "-delayed", timeout)
			ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			observer, err := fixture.Bus.Subscribe(ctx, comms.Inbox{
				Topic: messages.ControllerEvents(messages.DefaultControllerType), Name: "late-registration-observer", Mode: comms.TransientExclusive,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			requestCtx, cancelRequest := context.WithCancel(ctx)
			defer cancelRequest()
			finished := make(chan error, 1)
			go func() { _, err := fixture.Manager.Spawn(requestCtx, requestID, fixtures.Reference); finished <- err }()
			report := fixtures.AwaitChildReport(t, ctx, fixture.Storage)
			if canceled {
				cancelRequest()
			}
			select {
			case err := <-finished:
				want := context.DeadlineExceeded
				if canceled {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("initial wait returned %v; want %v", err, want)
				}
			case <-ctx.Done():
				t.Fatal("initial registration wait did not end")
			}
			// The child sends its only registration while no Spawn call is waiting.
			// This observer proves publication completed before the retry starts.
			if err := os.WriteFile(filepath.Join(report.Home, "register"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			snapshot := readAgentAnnouncement(t, ctx, observer, messages.AgentStarted, report.AgentID)
			if snapshot.Type != "fixture-agent" {
				t.Fatalf("unexpected registration: %+v", snapshot)
			}
			before := fixture.RequestCount()

			result, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)

			if err != nil {
				t.Fatalf("retry missed the child's registration: %v", err)
			}
			if result.AgentID != report.AgentID || nativeDetails(t, result).PID != report.PID || fixture.RequestCount() != before || len(fixtures.ChildReports(t, fixture.Storage)) != 1 {
				t.Fatal("retry did not reconcile the original process")
			}
		})
	}
}

func readAgentAnnouncement(t *testing.T, ctx context.Context, subscription comms.Subscription, kind messages.MessageType, expectedID string) agent.AgentSnapshot {
	t.Helper()
	for {
		delivery, err := subscription.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		message := delivery.Message()
		var snapshot agent.AgentSnapshot
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		if err := delivery.Ack(ctx); err != nil {
			t.Fatal(err)
		}
		if message.Type == kind && (expectedID == "" || snapshot.ID == expectedID) {
			return snapshot
		}
	}
}
