package packages_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/native"
)

const fixtureSelector = "github.com/defos-ai/fixture-agent@stable"

type packagePinningFixture struct {
	resolver       *packages.Resolver
	latest         atomic.Int32
	latestRequests atomic.Int32
	requests       atomic.Int32
	failDownload   atomic.Bool
}

func newPackagePinningFixture(t *testing.T) *packagePinningFixture {
	t.Helper()
	binary := nativeFixtureBinary(t)
	checksum := sha256.Sum256(binary)
	asset := "fixture-agent"
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	fixture := new(packagePinningFixture)
	fixture.latest.Store(1)
	fixture.resolver = packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		fixture.requests.Add(1)
		if request.URL.String() == "https://api.github.com/repos/defos-ai/fixture-agent/releases/latest" {
			fixture.latestRequests.Add(1)
			return response(request, http.StatusOK, fmt.Sprintf(`{"tag_name":"v0.%d.0","draft":false,"prerelease":false,"published_at":"2026-10-01T12:00:00Z"}`, fixture.latest.Load())), nil
		}
		for _, minor := range []int{1, 2} {
			version := fmt.Sprintf("v0.%d.0", minor)
			base := strings.Replace(fixtureReleaseURL, "v0.1.0", version, 1)
			switch request.URL.String() {
			case base + "agent.yaml":
				manifest := fmt.Sprintf("schema_version: 1\ntype: fixture-agent\nversion: %s\ndocker:\n  image: %s\nnative:\n  %s/%s:\n    asset: %s\n    sha256: %q\n", version, dockerFixtureImage, runtime.GOOS, runtime.GOARCH, asset, fmt.Sprintf("%x", checksum))
				return response(request, http.StatusOK, manifest), nil
			case base + asset:
				if fixture.failDownload.Load() {
					return response(request, http.StatusServiceUnavailable, "try again"), nil
				}
				return response(request, http.StatusOK, string(binary)), nil
			}
		}
		return response(request, http.StatusNotFound, "not found"), nil
	}))
	t.Cleanup(fixture.resolver.CloseIdleConnections)
	return fixture
}

func TestNativeRetryKeepsPackagePinAfterFailedDownloadAndRestart(t *testing.T) {
	fixture := newNativeFixture(t, "", 5*time.Second)
	source := newPackagePinningFixture(t)
	source.failDownload.Store(true)
	if err := fixture.manager.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.config.Resolver = source.resolver
	manager, err := native.NewManager(t.Context(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if manager != nil {
			if err := manager.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	const requestID = "task:download-retry"
	if _, err := manager.Spawn(t.Context(), requestID, "https://github.com/defos-ai/fixture-agent"); err == nil {
		t.Fatal("failed download unexpectedly launched a child")
	}
	source.latest.Store(2)
	if _, err := manager.Spawn(t.Context(), requestID, fixtureSelector); err == nil {
		t.Fatal("failed download unexpectedly launched a child on retry")
	}
	if source.latestRequests.Load() != 1 || len(childReports(t, fixture.storage)) != 0 {
		t.Fatal("retry resolved a different release or launched a child")
	}
	before := source.requests.Load()
	if _, err := manager.Spawn(t.Context(), requestID, fixtureReference); err == nil {
		t.Fatal("same request accepted a different selector")
	}
	if source.requests.Load() != before {
		t.Fatal("selector conflict performed another download")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err = native.NewManager(t.Context(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	source.failDownload.Store(false)
	result, err := manager.Spawn(t.Context(), requestID, "github.com/defos-ai/fixture-agent")
	if err != nil || result.Package != fixtureReference {
		t.Fatalf("restart did not retain the originally selected release: result=%+v error=%v", result, err)
	}
	if source.latestRequests.Load() != 1 || len(childReports(t, fixture.storage)) != 1 {
		t.Fatal("restart resolved Latest again or launched duplicate children")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err = native.NewManager(t.Context(), fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	before = source.requests.Load()
	if _, err := manager.Spawn(t.Context(), requestID, fixtureSelector); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("restart adopted or relaunched an uncertain native process: %v", err)
	}
	if source.requests.Load() != before {
		t.Fatal("uncertain native request downloaded again")
	}
}

func TestDockerRetryKeepsPackagePinAfterFailedPullAndRestart(t *testing.T) {
	fixture := newDockerManagerFixture(t)
	source := newPackagePinningFixture(t)
	fixture.config.Resolver = source.resolver
	manager := restartDockerManager(t, fixture)
	fixture.engine.pullErr = errors.New("fixture pull unavailable")
	const requestID = "task:pull-retry"
	if _, err := manager.Spawn(t.Context(), requestID, fixtureSelector); err == nil {
		t.Fatal("failed image pull unexpectedly launched a container")
	}
	source.latest.Store(2)
	fixture.manager = manager
	manager = restartDockerManager(t, fixture)
	fixture.engine.pullErr = nil
	result, err := manager.Spawn(t.Context(), requestID, "https://github.com/defos-ai/fixture-agent")
	if err != nil || result.Package != fixtureReference {
		t.Fatalf("restart did not retain the originally selected release: result=%+v error=%v", result, err)
	}
	before := source.requests.Load()
	repeated, err := manager.Spawn(t.Context(), requestID, "github.com/defos-ai/fixture-agent")
	if err != nil || repeated.ContainerID != result.ContainerID || source.requests.Load() != before {
		t.Fatalf("retry did not reuse the pinned container: result=%+v error=%v", repeated, err)
	}
	if _, err := manager.Spawn(t.Context(), requestID, strings.Replace(fixtureReference, "v0.1.0", "v0.2.0", 1)); err == nil {
		t.Fatal("same request accepted a conflicting package")
	}
	creates, starts, _ := fixture.engine.counts()
	if source.latestRequests.Load() != 1 || creates != 1 || starts != 1 {
		t.Fatalf("retry changed selection or duplicated launch: latest=%d creates=%d starts=%d", source.latestRequests.Load(), creates, starts)
	}
	fresh, err := manager.Spawn(t.Context(), "task:fresh-selection", fixtureSelector)
	if err != nil || fresh.Package != strings.Replace(fixtureReference, "v0.1.0", "v0.2.0", 1) || source.latestRequests.Load() != 2 {
		t.Fatalf("new request did not select the updated release: result=%+v error=%v", fresh, err)
	}
}

func TestPackageSelectionRejectsCorruptStateWithoutResolvingAgain(t *testing.T) {
	source := newPackagePinningFixture(t)
	for _, test := range []struct {
		name, selector string
		change         func(map[string]any) []byte
	}{
		{"incomplete", fixtureSelector, func(map[string]any) []byte { return []byte(`{"request_id":`) }},
		{"wrong request", fixtureSelector, func(saved map[string]any) []byte {
			saved["request_id"] = "other"
			data, _ := json.Marshal(saved)
			return data
		}},
		{"different repository", fixtureSelector, func(saved map[string]any) []byte {
			saved["package"] = "github.com/other/agent@v0.1.0"
			data, _ := json.Marshal(saved)
			return data
		}},
		{"changed exact version", fixtureReference, func(saved map[string]any) []byte {
			saved["package"] = strings.Replace(fixtureReference, "v0.1.0", "v0.2.0", 1)
			data, _ := json.Marshal(saved)
			return data
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Mkdir(filepath.Join(directory, "records"), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := runtimes.PinPackage(t.Context(), directory, "request", test.selector, source.resolver); err != nil {
				t.Fatal(err)
			}
			paths, err := filepath.Glob(filepath.Join(directory, "records", "*.package.json"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("missing durable package selection: %v", err)
			}
			data, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			var saved map[string]any
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			corrupt := test.change(saved)
			if err := os.WriteFile(paths[0], corrupt, 0600); err != nil {
				t.Fatal(err)
			}
			before := source.requests.Load()
			if _, err := runtimes.PinPackage(t.Context(), directory, "request", test.selector, source.resolver); err == nil {
				t.Fatal("corrupt selection was trusted")
			}
			after, err := os.ReadFile(paths[0])
			if err != nil || string(after) != string(corrupt) || source.requests.Load() != before {
				t.Fatal("corrupt selection was overwritten or caused another resolution")
			}
		})
	}
}

func TestConcurrentPackageSelectionsKeepTheWinningPin(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "records"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	t.Cleanup(func() { cancel(); workers.Wait() })
	var resolutions atomic.Int32
	ready, release := make(chan struct{}, 2), make(chan struct{})
	resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		version := resolutions.Add(1)
		ready <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		return response(request, http.StatusOK, fmt.Sprintf(`{"tag_name":"v0.%d.0","draft":false,"prerelease":false,"published_at":"2026-10-01T12:00:00Z"}`, version)), nil
	}))
	defer resolver.CloseIdleConnections()
	type selectionResult struct {
		pin string
		err error
	}
	results := make(chan selectionResult, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			pin, err := runtimes.PinPackage(ctx, directory, "same-request", fixtureSelector, resolver)
			results <- selectionResult{pin, err}
		}()
	}
	for range 2 {
		select {
		case <-ctx.Done():
			t.Fatal("concurrent selections did not resolve")
		case <-ready:
		}
	}
	close(release)
	first, second := <-results, <-results
	winner, err := runtimes.PinPackage(ctx, directory, "same-request", fixtureSelector, resolver)
	if err != nil || resolutions.Load() != 2 {
		t.Fatalf("retry did not reuse winning selection: %q %v", winner, err)
	}
	if first.err != nil && second.err != nil {
		t.Fatalf("neither selection succeeded: %v; %v", first.err, second.err)
	}
	for _, result := range []selectionResult{first, second} {
		if result.err == nil && result.pin != winner {
			t.Fatalf("concurrent selection overwrote a successful pin: got %q, winner %q", result.pin, winner)
		}
	}
}
