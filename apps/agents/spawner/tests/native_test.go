package packages_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/native"
	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

const fixtureReference = "github.com/defos-ai/fixture-agent@v0.1.0"

var fixtureBuild struct {
	once      sync.Once
	directory string
	binary    []byte
	err       error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if fixtureBuild.directory != "" {
		_ = os.RemoveAll(fixtureBuild.directory)
	}
	os.Exit(code)
}

func nativeFixtureBinary(t *testing.T) []byte {
	t.Helper()
	fixtureBuild.once.Do(func() {
		fixtureBuild.directory, fixtureBuild.err = os.MkdirTemp("", "commander-spawner-fixture-")
		if fixtureBuild.err != nil {
			return
		}
		path := filepath.Join(fixtureBuild.directory, "fixture-agent")
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "go", "build", "-o", path, "./fixture")
		output, err := command.CombinedOutput()
		if err != nil {
			fixtureBuild.err = fmt.Errorf("build local agent fixture: %w: %s", err, output)
			return
		}
		fixtureBuild.binary, fixtureBuild.err = os.ReadFile(path)
	})
	if fixtureBuild.err != nil {
		t.Fatal(fixtureBuild.err)
	}
	return fixtureBuild.binary
}

func nativeFixtureResolver(t *testing.T, mode string) (*packages.Resolver, *atomic.Int32) {
	t.Helper()
	binary := nativeFixtureBinary(t)
	asset := "fixture-agent" + mode
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	checksum := sha256.Sum256(binary)
	manifest := fmt.Sprintf("schema_version: 1\ntype: fixture-agent\nversion: v0.1.0\nnative:\n  %s/%s:\n    asset: %s\n    sha256: %q\n", runtime.GOOS, runtime.GOARCH, asset, fmt.Sprintf("%x", checksum))
	requests := new(atomic.Int32)
	resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		switch filepath.Base(request.URL.Path) {
		case "agent.yaml":
			return response(request, http.StatusOK, manifest), nil
		case asset:
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(binary)), Request: request}, nil
		default:
			return response(request, http.StatusNotFound, "not found"), nil
		}
	}))
	t.Cleanup(resolver.CloseIdleConnections)
	return resolver, requests
}

type nativeFixture struct {
	manager  *native.Manager
	config   native.Config
	bus      *fscomms.FsBus
	storage  string
	requests *atomic.Int32
}

func newNativeFixture(t *testing.T, mode string, timeout time.Duration) nativeFixture {
	t.Helper()
	root := t.TempDir()
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
	})
	resolver, requests := nativeFixtureResolver(t, mode)
	config := native.Config{
		Directory: filepath.Join(root, "spawner"), Bus: bus, BusURL: filepath.Join(root, "bus"),
		StorageURL: filepath.Join(root, "storage"), Resolver: resolver, RegistrationTimeout: timeout,
		Environment: map[string]string{"AWS_REGION": "fixture-region"},
	}
	manager, err := native.NewManager(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	return nativeFixture{manager: manager, config: config, bus: bus, storage: config.StorageURL, requests: requests}
}

type childReport struct {
	AgentID         string `json:"agent_id"`
	AgentIDSuffix   string `json:"agent_id_suffix"`
	PID             int    `json:"pid"`
	InheritedSecret bool   `json:"inherited_secret"`
	Region          string `json:"region"`
	Home            string `json:"home"`
}

func childReports(t *testing.T, directory string) []childReport {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(directory, "*.started"))
	if err != nil {
		t.Fatal(err)
	}
	reports := make([]childReport, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var report childReport
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		reports = append(reports, report)
	}
	return reports
}

func TestNativeSpawnUsesAssignedIdentityAndReusesOnlyItsLiveChild(t *testing.T) {
	t.Setenv("COMMANDER_AGENT_ID_SUFFIX", "12345678")
	for _, key := range []string{"SPAWNER_PARENT_SECRET", "SECRETS_ENCRYPTION_KEY", "API_PASSWORD", "MCP_TOKEN"} {
		t.Setenv(key, "private-parent-sentinel")
	}
	fixture := newNativeFixture(t, "", 5*time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	first, err := fixture.manager.Spawn(ctx, "task:"+id.New(), fixtureReference)
	if err != nil {
		t.Fatal(err)
	}
	if first.AgentType != "fixture-agent" || !strings.HasPrefix(first.AgentID, "fixture-agent-") || !id.ValidAgent(first.AgentID) || first.Package != fixtureReference || first.PID <= 0 {
		t.Fatalf("unexpected spawned identity: %+v", first)
	}
	if len(first.Actions) != 1 || first.Actions[0].Name != "fixture-action" || first.Actions[0].Status != actionmodel.Ready {
		t.Fatalf("registration action inventory was not returned: %+v", first.Actions)
	}
	requestID := "task:" + id.New()
	second, err := fixture.manager.Spawn(ctx, requestID, fixtureReference)
	if err != nil {
		t.Fatal(err)
	}
	if second.AgentID == first.AgentID || second.PID == first.PID {
		t.Fatal("independent requests reused an agent identity or process")
	}
	before := fixture.requests.Load()
	repeated, err := fixture.manager.Spawn(ctx, requestID, fixtureReference)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.AgentID != second.AgentID || repeated.PID != second.PID || fixture.requests.Load() != before {
		t.Fatal("same request did not reuse the existing child without downloading again")
	}
	if _, err := fixture.manager.Spawn(ctx, requestID, "github.com/defos-ai/other-agent@v0.1.0"); err == nil {
		t.Fatal("request identity accepted a conflicting package")
	}
	reports := childReports(t, fixture.storage)
	if len(reports) != 2 {
		t.Fatalf("started %d children; want two", len(reports))
	}
	for _, report := range reports {
		if len(report.AgentIDSuffix) != 8 || report.AgentID != "fixture-agent-"+report.AgentIDSuffix || !id.ValidAgent(report.AgentID) {
			t.Fatalf("child did not construct its assigned identity from a hex suffix: %+v", report)
		}
		if report.InheritedSecret || report.Region != "fixture-region" || report.Home == "" {
			t.Fatalf("unexpected child environment: %+v", report)
		}
		if relative, err := filepath.Rel(fixture.config.Directory, report.Home); err != nil || !filepath.IsLocal(relative) {
			t.Fatalf("child home is not isolated within its spawner directory: %s", report.Home)
		}
	}
	if err := filepath.WalkDir(filepath.Join(fixture.config.Directory, "records"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("private-parent-sentinel")) || bytes.Contains(data, []byte("fixture-region")) {
			t.Error("launch journal stores environment values")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Close(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, report := range reports {
			if _, err := os.Stat(filepath.Join(fixture.storage, report.AgentID+".stopped")); err != nil {
				t.Fatalf("Close returned before child shutdown finished: %v", err)
			}
		}
	}
}

func TestNativeRejectsInvalidRegistrationsWithoutRelaunching(t *testing.T) {
	for _, mode := range []string{"-wrong-type", "-wrong-id", "-invalid-schema"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newNativeFixture(t, mode, time.Second)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			requestID := "task:" + id.New()
			if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil {
				t.Fatal("registration with an incorrect identity, type, or action schema was accepted")
			}
			before := fixture.requests.Load()
			if _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); err == nil {
				t.Fatal("retry accepted a mismatched registration")
			}
			if fixture.requests.Load() != before || len(childReports(t, fixture.storage)) != 1 {
				t.Fatal("timed-out request started another process")
			}
		})
	}
}

func TestNativeCanceledWaitRetainsOneOwnedLaunch(t *testing.T) {
	fixture := newNativeFixture(t, "-silent", 5*time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	requestID := "task:" + id.New()
	finished := make(chan error, 1)
	go func() { _, err := fixture.manager.Spawn(ctx, requestID, fixtureReference); finished <- err }()
	defer cancel()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		paths, err := filepath.Glob(filepath.Join(fixture.storage, "*.started"))
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
	before := fixture.requests.Load()
	retryCtx, retryCancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer retryCancel()
	if _, err := fixture.manager.Spawn(retryCtx, requestID, fixtureReference); err == nil {
		t.Fatal("silent child unexpectedly registered")
	}
	if fixture.requests.Load() != before || len(childReports(t, fixture.storage)) != 1 {
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
			fixture := newNativeFixture(t, "-delayed", timeout)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			observer, err := fixture.bus.Subscribe(ctx, comms.Inbox{
				Topic: messages.ControllerEvents(messages.DefaultControllerType), Name: "late-registration-observer", Mode: comms.TransientExclusive,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			requestCtx, cancelRequest := context.WithCancel(ctx)
			defer cancelRequest()
			requestID := "task:" + id.New()
			finished := make(chan error, 1)
			go func() { _, err := fixture.manager.Spawn(requestCtx, requestID, fixtureReference); finished <- err }()
			report := awaitChildReport(t, ctx, fixture.storage)
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
			before := fixture.requests.Load()
			result, err := fixture.manager.Spawn(ctx, requestID, fixtureReference)
			if err != nil {
				t.Fatalf("retry missed the child's registration: %v", err)
			}
			if result.AgentID != report.AgentID || result.PID != report.PID || fixture.requests.Load() != before || len(childReports(t, fixture.storage)) != 1 {
				t.Fatal("retry did not reconcile the original process")
			}
		})
	}
}

func awaitChildReport(t *testing.T, ctx context.Context, directory string) childReport {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		reports := childReports(t, directory)
		if len(reports) == 1 {
			return reports[0]
		}
		select {
		case <-ctx.Done():
			t.Fatal("fixture child did not start")
		case <-tick.C:
		}
	}
}

func TestNativeRestartDoesNotRelaunchRecordedRequest(t *testing.T) {
	fixture := newNativeFixture(t, "", 5*time.Second)
	requestID := "task:" + id.New()
	if _, err := fixture.manager.Spawn(t.Context(), requestID, fixtureReference); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := native.NewManager(t.Context(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	before := fixture.requests.Load()
	if _, err := restarted.Spawn(t.Context(), requestID, fixtureReference); err == nil {
		t.Fatal("restarted manager trusted an old process identity")
	}
	if fixture.requests.Load() != before || len(childReports(t, fixture.storage)) != 1 {
		t.Fatal("restarted manager relaunched a recorded request")
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
