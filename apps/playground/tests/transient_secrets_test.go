package tests

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestTransientInputStaysScopedAndIsNotResentAfterUncertainSubmission(t *testing.T) {
	fake := fakes.NewTransientSecrets(time.Now().UTC().Add(time.Minute))
	fake.PostStatus(http.StatusServiceUnavailable)
	releaseDiscovery := make(chan struct{})
	discovery := fake.HoldDiscovery(releaseDiscovery)
	playground := fixtures.NewTransientPlayground(t, fake)
	awaitView(t, playground, "Ask about missions")
	playground.Command("/watch " + fakes.OperationID)
	select {
	case <-discovery:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime requirements did not query request metadata")
	}
	if view := playground.View(); strings.Contains(view, "Shared secrets") {
		t.Fatal("unclassified runtime requirement opened reusable input")
	}
	close(releaseDiscovery)
	view := awaitView(t, playground, "CODE")
	if !strings.Contains(view, fakes.RequestID) || strings.Contains(view, fakes.OtherRequestID) {
		t.Fatalf("one-time input did not select the watched request: %s", view)
	}
	value := "fixture-verification-value"

	playground.Text(value)
	playground.Key(tea.KeyEnter)
	awaitView(t, playground, "outcome is uncertain")
	playground.Key(tea.KeyEsc)
	awaitView(t, playground, "Ask about missions")
	playground.Command("/secrets")
	awaitView(t, playground, "One-time secret")
	awaitView(t, playground, "will not be resent")
	reads := fake.DetailReads()
	playground.Text("r")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err := playground.Await(ctx, func(string) bool { return fake.DetailReads() > reads })
	cancel()
	if err != nil {
		t.Fatal("explicit status check did not read request metadata")
	}
	playground.Text("another-fixture-value")
	fake.Status("fulfilled")
	playground.Text("r")
	awaitView(t, playground, "Received by agent. This does not confirm")
	playground.Key(tea.KeyEnter)
	awaitView(t, playground, "Ask about missions")
	playground.Command("Hello after the secret form")
	awaitView(t, playground, "Fixture reply")
	playground.Close()

	submissions, managedWrites, modelBodies, mcpArguments := fake.Snapshot()
	if len(submissions) != 1 || submissions[0].RequestID != fakes.RequestID || submissions[0].Value != value {
		t.Fatal("expected one direct submission to the watched request, with its entered value")
	}
	if managedWrites != 0 {
		t.Fatalf("one-time input caused %d reusable-secret writes", managedWrites)
	}
	assertPrivateInput(t, playground, value, append(modelBodies, mcpArguments...))
	assertPrivateInput(t, playground, "another-fixture-value", append(modelBodies, mcpArguments...))
}

func TestTransientInputExpiresWithoutSavingItsDraft(t *testing.T) {
	fake := fakes.NewTransientSecrets(time.Now().UTC().Add(3 * time.Second))
	playground := fixtures.NewTransientPlayground(t, fake)
	awaitView(t, playground, "Ask about missions")
	playground.Command("/watch " + fakes.OperationID)
	awaitView(t, playground, "CODE")
	value := "fixture-expiring-value"

	playground.Text(value)
	awaitView(t, playground, "expired")
	playground.Key(tea.KeyEnter)
	awaitView(t, playground, "Ask about missions")
	playground.Close()

	submissions, managedWrites, modelBodies, mcpArguments := fake.Snapshot()
	if len(submissions) != 0 || managedWrites != 0 {
		t.Fatal("expired one-time input must not submit or save a draft")
	}
	assertPrivateInput(t, playground, value, append(modelBodies, mcpArguments...))
}

func TestClosingPlaygroundCancelsAndJoinsOneTimeSubmission(t *testing.T) {
	fake := fakes.NewTransientSecrets(time.Now().UTC().Add(time.Minute))
	started, canceled := fake.HoldPost()
	playground := fixtures.NewTransientPlayground(t, fake)
	awaitView(t, playground, "Ask about missions")
	playground.Command("/watch " + fakes.OperationID)
	awaitView(t, playground, "CODE")
	value := "fixture-cancelled-value"

	playground.Text(value)
	playground.Key(tea.KeyEnter)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("one-time submission did not reach the direct API")
	}
	closed := make(chan struct{})
	go func() { playground.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Playground did not join its canceled one-time submission")
	}

	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("closing Playground did not cancel the in-flight API request")
	}
	submissions, managedWrites, modelBodies, mcpArguments := fake.Snapshot()
	if len(submissions) != 1 || managedWrites != 0 {
		t.Fatal("closing must neither retry nor save a one-time value")
	}
	assertPrivateInput(t, playground, value, append(modelBodies, mcpArguments...))
}

func TestRuntimeReusableRequestStillUsesManagedSecretInput(t *testing.T) {
	fake := fakes.NewReusableSecrets(time.Now().UTC().Add(time.Minute))
	playground := fixtures.NewTransientPlayground(t, fake)
	awaitView(t, playground, "Ask about missions")
	playground.Command("/watch " + fakes.OperationID)
	view := awaitView(t, playground, "CODE")
	if !strings.Contains(view, "Shared secrets") || strings.Contains(view, "One-time secret") {
		t.Fatal("confirmed reusable runtime requirement must use the shared-secret form")
	}
	value := "fixture-reusable-value"

	playground.Text(value)
	playground.Key(tea.KeyEnter)
	awaitView(t, playground, "Secrets saved. Waiting for the agent.")
	playground.Close()

	submissions, managedWrites, modelBodies, mcpArguments := fake.Snapshot()
	if len(submissions) != 0 || managedWrites != 1 || fake.ManagedValue() != value {
		t.Fatal("reusable runtime request must save once through managed-secret storage")
	}
	assertPrivateInput(t, playground, value, append(modelBodies, mcpArguments...))
}

func awaitView(t *testing.T, playground *fixtures.Playground, text string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	view, err := playground.Await(ctx, func(view string) bool { return strings.Contains(view, text) })
	if err != nil {
		t.Fatalf("waiting for %q: %s", text, view)
	}
	return view
}

func assertPrivateInput(t *testing.T, playground *fixtures.Playground, value string, transport []string) {
	t.Helper()
	views, histories, savedKeys := playground.Evidence()
	if len(views) == 0 || len(histories) == 0 {
		t.Fatal("expected rendered and persisted chat evidence for the privacy check")
	}
	for _, view := range append(views, transport...) {
		if strings.Contains(view, value) {
			t.Fatal("one-time value leaked into rendered output or model/MCP transport")
		}
	}
	for _, history := range histories {
		if strings.Contains(string(history), value) {
			t.Fatal("one-time value leaked into saved chat history")
		}
	}
	if savedKeys != 0 {
		t.Fatal("one-time input reached the saved provider-key callback")
	}
}
