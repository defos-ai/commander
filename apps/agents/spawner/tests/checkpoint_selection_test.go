package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/id"
)

func TestCheckpointPackagePinSurvivesUncertainSave(t *testing.T) {
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	var requests atomic.Int32
	resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return fixtures.Response(request, http.StatusOK, `{"tag_name":"v0.1.0","draft":false,"prerelease":false,"published_at":"2026-10-01T12:00:00Z"}`), nil
	}))
	defer resolver.CloseIdleConnections()
	uncertain := errors.New("checkpoint acknowledgement lost")
	var acknowledge atomic.Bool
	journal.SetAfterSave(func(context.Context, string, json.RawMessage) error {
		if !acknowledge.Load() {
			return uncertain
		}
		return nil
	})
	if pin, err := runtimes.PinPackage(ctx, requestID, fixtureSelector, resolver); !errors.Is(err, uncertain) || pin != "" {
		t.Fatalf("unconfirmed selection allowed preparation: %q, %v", pin, err)
	}
	// Reading saved bytes alone cannot confirm an earlier uncertain write.
	if _, err := runtimes.PinPackage(ctx, requestID, fixtureSelector, resolver); !errors.Is(err, uncertain) {
		t.Fatalf("saved selection bypassed the persistence acknowledgement: %v", err)
	}
	acknowledge.Store(true)
	changedLatest := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		t.Error("saved execution resolved Latest again")
		return fixtures.Response(request, http.StatusOK, `{"tag_name":"v0.2.0","draft":false,"prerelease":false,"published_at":"2026-10-02T12:00:00Z"}`), nil
	}))
	defer changedLatest.CloseIdleConnections()
	pin, err := runtimes.PinPackage(ctx, requestID, "https://github.com/defos-ai/fixture-agent", changedLatest)
	if err != nil || pin != fixtures.Reference || requests.Load() != 1 {
		t.Fatalf("checkpoint did not retain its original package: %q, %v", pin, err)
	}
	if _, err := runtimes.PinPackage(ctx, requestID, fixtures.Reference, changedLatest); err == nil {
		t.Fatal("same execution accepted a different requested selector")
	}
}

func TestPackageSelectionRequiresExecutionCheckpoints(t *testing.T) {
	var requests atomic.Int32
	resolver := packages.NewResolver(fakes.Transport(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected network request")
	}))
	defer resolver.CloseIdleConnections()
	if _, err := runtimes.PinPackage(t.Context(), "startup:0", fixtures.Reference, resolver); err == nil {
		t.Fatal("package selection accepted a request outside a controller execution")
	}
	commandID := id.MessageID()
	ctx := action.WithExecution(t.Context(), action.Execution{CommandID: commandID})
	if _, err := runtimes.PinPackage(ctx, "command:"+commandID, fixtures.Reference, resolver); !errors.Is(err, action.ErrCheckpointsUnavailable) {
		t.Fatalf("execution without checkpoint capability did not fail closed: %v", err)
	}
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	journal.ReplaceCheckpoint("package-selection", json.RawMessage(`{"package":"github.com/other/agent@v0.1.0"}`))
	if _, err := runtimes.PinPackage(ctx, requestID, fixtures.Reference, resolver); err == nil {
		t.Fatal("invalid saved pin was accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("missing/invalid checkpoint access triggered package resolution")
	}
}
