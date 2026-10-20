package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/id"
)

func TestCheckpointPackagePinSurvivesUncertainSaveAndLocalStateLoss(t *testing.T) {
	ctx, requestID, journal := newSpawnCheckpoints(t)
	var requests atomic.Int32
	resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return response(request, http.StatusOK, `{"tag_name":"v0.1.0","draft":false,"prerelease":false,"published_at":"2026-10-01T12:00:00Z"}`), nil
	}))
	defer resolver.CloseIdleConnections()
	uncertain := errors.New("checkpoint acknowledgement lost")
	var acknowledge atomic.Bool
	journal.afterSave = func(context.Context, string, json.RawMessage) error {
		if !acknowledge.Load() {
			return uncertain
		}
		return nil
	}
	directory := t.TempDir()
	if pin, err := runtimes.PinPackage(ctx, directory, requestID, fixtureSelector, resolver); !errors.Is(err, uncertain) || pin != "" {
		t.Fatalf("unconfirmed selection allowed preparation: %q, %v", pin, err)
	}
	// Reading saved bytes alone cannot confirm an earlier uncertain write.
	if _, err := runtimes.PinPackage(ctx, directory, requestID, fixtureSelector, resolver); !errors.Is(err, uncertain) {
		t.Fatalf("saved selection bypassed the persistence acknowledgement: %v", err)
	}
	acknowledge.Store(true)
	changedLatest := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		t.Error("saved execution resolved Latest again")
		return response(request, http.StatusOK, `{"tag_name":"v0.2.0","draft":false,"prerelease":false,"published_at":"2026-10-02T12:00:00Z"}`), nil
	}))
	defer changedLatest.CloseIdleConnections()
	pin, err := runtimes.PinPackage(ctx, t.TempDir(), requestID, "https://github.com/defos-ai/fixture-agent", changedLatest)
	if err != nil || pin != fixtureReference || requests.Load() != 1 {
		t.Fatalf("checkpoint did not retain its original package: %q, %v", pin, err)
	}
	if _, err := runtimes.PinPackage(ctx, directory, requestID, fixtureReference, changedLatest); err == nil {
		t.Fatal("same execution accepted a different requested selector")
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatalf("checkpoint selection created local launch state: %v", err)
	}
}

func TestExecutionPackageSelectionNeverFallsBackToLocalRecords(t *testing.T) {
	var requests atomic.Int32
	resolver := packages.NewResolver(transportFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected network request")
	}))
	defer resolver.CloseIdleConnections()
	commandID := id.MessageID()
	ctx := action.WithExecution(t.Context(), action.Execution{CommandID: commandID})
	directory := t.TempDir()
	if _, err := runtimes.PinPackage(ctx, directory, "command:"+commandID, fixtureReference, resolver); !errors.Is(err, action.ErrCheckpointsUnavailable) {
		t.Fatalf("execution without checkpoint capability did not fail closed: %v", err)
	}
	ctx, requestID, journal := newSpawnCheckpoints(t)
	journal.record.Checkpoints = map[string]json.RawMessage{
		"package-selection": json.RawMessage(`{"package":"github.com/other/agent@v0.1.0"}`),
	}
	if _, err := runtimes.PinPackage(ctx, directory, requestID, fixtureReference, resolver); err == nil {
		t.Fatal("invalid saved pin was accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("missing/invalid checkpoint access triggered package resolution")
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatalf("missing/invalid checkpoint access fell back to file records: %v", err)
	}
}
