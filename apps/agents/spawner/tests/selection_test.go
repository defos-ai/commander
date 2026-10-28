package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/native"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
)

const fixtureSelector = "github.com/defos-ai/fixture-agent@stable"

func TestNativeRetryKeepsPackagePinAfterFailedDownloadAndRestart(t *testing.T) {
	fixture := fixtures.NewNativeManager(t, "", 5*time.Second)
	source := fixtures.NewPackageSource(t)
	source.SetDownloadFailure(true)
	if err := fixture.Manager.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.Config.Resolver = source.Resolver()
	manager, err := native.NewManager(t.Context(), fixture.Config)
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
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
	if _, err := manager.Spawn(ctx, requestID, "https://github.com/defos-ai/fixture-agent"); err == nil {
		t.Fatal("failed download unexpectedly launched a child")
	}
	source.SetLatest(2)
	if _, err := manager.Spawn(ctx, requestID, fixtureSelector); err == nil {
		t.Fatal("failed download unexpectedly launched a child on retry")
	}
	if source.LatestRequestCount() != 1 || len(fixtures.ChildReports(t, fixture.Storage)) != 0 {
		t.Fatal("retry resolved a different release or launched a child")
	}
	before := source.RequestCount()
	if _, err := manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
		t.Fatal("same request accepted a different selector")
	}
	if source.RequestCount() != before {
		t.Fatal("selector conflict performed another download")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err = native.NewManager(t.Context(), fixture.Config)
	if err != nil {
		t.Fatal(err)
	}
	source.SetDownloadFailure(false)

	result, err := manager.Spawn(ctx, requestID, "github.com/defos-ai/fixture-agent")

	if err != nil || result.Package != fixtures.Reference {
		t.Fatalf("restart did not retain the originally selected release: result=%+v error=%v", result, err)
	}
	if source.LatestRequestCount() != 1 || len(fixtures.ChildReports(t, fixture.Storage)) != 1 {
		t.Fatal("restart resolved Latest again or launched duplicate children")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err = native.NewManager(t.Context(), fixture.Config)
	if err != nil {
		t.Fatal(err)
	}
	before = source.RequestCount()
	if _, err := manager.Spawn(ctx, requestID, fixtureSelector); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("restart adopted or relaunched an uncertain native process: %v", err)
	}
	if source.RequestCount() != before {
		t.Fatal("uncertain native request downloaded again")
	}
}

func TestDockerRetryKeepsPackagePinAfterFailedPullAndRestart(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	source := fixtures.NewPackageSource(t)
	fixture.Config.Resolver = source.Resolver()
	manager := fixtures.RestartDockerManager(t, fixture)
	fixture.Engine.SetPullError(errors.New("fixture pull unavailable"))
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
	if _, err := manager.Spawn(ctx, requestID, fixtureSelector); err == nil {
		t.Fatal("failed image pull unexpectedly launched a container")
	}
	source.SetLatest(2)
	fixture.Manager = manager
	manager = fixtures.RestartDockerManager(t, fixture)
	fixture.Engine.SetPullError(nil)

	result, err := manager.Spawn(ctx, requestID, "https://github.com/defos-ai/fixture-agent")

	if err != nil || result.Package != fixtures.Reference {
		t.Fatalf("restart did not retain the originally selected release: result=%+v error=%v", result, err)
	}
	before := source.RequestCount()

	repeated, err := manager.Spawn(ctx, requestID, "github.com/defos-ai/fixture-agent")

	if err != nil || dockerDetails(t, repeated).ContainerID != dockerDetails(t, result).ContainerID || source.RequestCount() != before {
		t.Fatalf("retry did not reuse the pinned container: result=%+v error=%v", repeated, err)
	}
	if _, err := manager.Spawn(ctx, requestID, strings.Replace(fixtures.Reference, "v0.1.0", "v0.2.0", 1)); err == nil {
		t.Fatal("same request accepted a conflicting package")
	}
	creates, starts, _ := fixture.Engine.Counts()
	if source.LatestRequestCount() != 1 || creates != 1 || starts != 1 {
		t.Fatalf("retry changed selection or duplicated launch: latest=%d creates=%d starts=%d", source.LatestRequestCount(), creates, starts)
	}
	freshCtx, freshRequest, _ := fixtures.SpawnCheckpoints(t)
	fresh, err := manager.Spawn(freshCtx, freshRequest, fixtureSelector)
	if err != nil || fresh.Package != strings.Replace(fixtures.Reference, "v0.1.0", "v0.2.0", 1) || source.LatestRequestCount() != 2 {
		t.Fatalf("new request did not select the updated release: result=%+v error=%v", fresh, err)
	}
}

func TestPackageSelectionRejectsCorruptStateWithoutResolvingAgain(t *testing.T) {
	source := fixtures.NewPackageSource(t)
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
		{"changed exact version", fixtures.Reference, func(saved map[string]any) []byte {
			saved["package"] = strings.Replace(fixtures.Reference, "v0.1.0", "v0.2.0", 1)
			data, _ := json.Marshal(saved)
			return data
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
			if _, err := runtimes.PinPackage(ctx, requestID, test.selector, source.Resolver()); err != nil {
				t.Fatal(err)
			}
			data, found, err := journal.LoadCheckpoint(ctx, "package-selection")
			if err != nil || !found {
				t.Fatalf("missing durable package selection: %v", err)
			}
			var saved map[string]any
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			corrupt := test.change(saved)
			journal.ReplaceCheckpoint("package-selection", corrupt)
			before := source.RequestCount()
			if _, err := runtimes.PinPackage(ctx, requestID, test.selector, source.Resolver()); err == nil {
				t.Fatal("corrupt selection was trusted")
			}
			after, _, err := journal.LoadCheckpoint(ctx, "package-selection")
			if err != nil || string(after) != string(corrupt) || source.RequestCount() != before {
				t.Fatal("corrupt selection was overwritten or caused another resolution")
			}
		})
	}
}

func TestConcurrentPackageSelectionsKeepTheWinningPin(t *testing.T) {
	ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	t.Cleanup(func() { cancel(); workers.Wait() })
	var resolutions atomic.Int32
	ready, release := make(chan struct{}, 2), make(chan struct{})
	resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		version := resolutions.Add(1)
		ready <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		return fixtures.Response(request, http.StatusOK, fmt.Sprintf(`{"tag_name":"v0.%d.0","draft":false,"prerelease":false,"published_at":"2026-10-01T12:00:00Z"}`, version)), nil
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
			pin, err := runtimes.PinPackage(ctx, requestID, fixtureSelector, resolver)
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
	winner, err := runtimes.PinPackage(ctx, requestID, fixtureSelector, resolver)
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
