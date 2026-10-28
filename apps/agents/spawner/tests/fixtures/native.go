package fixtures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/native"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/framework/domain/secrets"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

var fixtureBuild struct {
	once      sync.Once
	directory string
	binary    []byte
	err       error
}

func NativeBinary(t *testing.T) []byte {
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
	return bytes.Clone(fixtureBuild.binary)
}

func nativeResolver(t *testing.T, mode string) (*packages.Resolver, *atomic.Int32) {
	t.Helper()
	binary := NativeBinary(t)
	asset := "fixture-agent" + mode
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	checksum := sha256.Sum256(binary)
	agentType := "fixture-agent"
	if mode == "-builder" {
		agentType = "builder-agent"
	}
	manifest := fmt.Sprintf("schema_version: 1\ntype: %s\nversion: v0.1.0\nnative:\n  %s/%s:\n    asset: %s\n    sha256: %q\n", agentType, runtime.GOOS, runtime.GOARCH, asset, fmt.Sprintf("%x", checksum))
	requests := new(atomic.Int32)
	resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		switch request.URL.String() {
		case FixtureReleaseURL + "agent.yaml", FixtureModuleReleaseURL + "agent.yaml", BuilderReleaseURL + "agent.yaml":
			return Response(request, http.StatusOK, manifest), nil
		case FixtureReleaseURL + asset, FixtureModuleReleaseURL + asset, BuilderReleaseURL + asset:
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(binary)), Request: request}, nil
		default:
			return Response(request, http.StatusNotFound, "not found"), nil
		}
	}))
	t.Cleanup(resolver.CloseIdleConnections)
	return resolver, requests
}

type NativeManager struct {
	Manager  *native.Manager
	Config   native.Config
	Bus      *fscomms.FsBus
	Storage  string
	requests *atomic.Int32
}

func NewNativeManager(t *testing.T, mode string, timeout time.Duration, bootstrap ...*runtimes.SecretBootstrap) NativeManager {
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
	resolver, requests := nativeResolver(t, mode)
	config := native.Config{
		Directory: filepath.Join(root, "spawner"), Bus: bus, BusURL: filepath.Join(root, "bus"),
		StorageURL: filepath.Join(root, "storage"), Resolver: resolver, RegistrationTimeout: timeout,
		Environment: map[string]string{
			"AWS_REGION": "fixture-region", "CHROMIUM_PATH": filepath.Join(root, "chromium"), "CHROMIUM_NO_SANDBOX": "true",
		},
	}
	if len(bootstrap) > 0 {
		config.SecretBootstrap = bootstrap[0]
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
	return NativeManager{Manager: manager, Config: config, Bus: bus, Storage: config.StorageURL, requests: requests}
}

type ChildReport struct {
	AgentID           string          `json:"agent_id"`
	AgentIDSuffix     string          `json:"agent_id_suffix"`
	PID               int             `json:"pid"`
	InheritedSecret   bool            `json:"inherited_secret"`
	SecretChallenge   *secrets.Secret `json:"secret_challenge"`
	Region            string          `json:"region"`
	Home              string          `json:"home"`
	ChromiumPath      string          `json:"chromium_path"`
	ChromiumNoSandbox string          `json:"chromium_no_sandbox"`
}

func ChildReports(t *testing.T, directory string) []ChildReport {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(directory, "*.started"))
	if err != nil {
		t.Fatal(err)
	}
	reports := make([]ChildReport, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var report ChildReport
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		reports = append(reports, report)
	}
	return reports
}

func AwaitChildReport(t *testing.T, ctx context.Context, directory string) ChildReport {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		reports := ChildReports(t, directory)
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

func CleanupNativeBuild() {
	if fixtureBuild.directory != "" {
		_ = os.RemoveAll(fixtureBuild.directory)
	}
}
func (f NativeManager) RequestCount() int32 { return f.requests.Load() }
