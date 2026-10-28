package packages_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/docker"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

func TestDockerProgressPreservesLaunchIntentAndObservedReadiness(t *testing.T) {
	for _, logErr := range []error{nil, errors.New("private-progress-backend-diagnostic")} {
		name := "available"
		if logErr != nil {
			name = "publication unavailable"
		}
		t.Run(name, func(t *testing.T) {
			fixture := fixtures.NewDockerManager(t)
			fixture.Engine.SetOnStart(func(ctx context.Context, container docker.Container) error {
				return fixtures.PublishDockerRegistration(ctx, fixture.Bus, container, func(snapshot *agent.AgentSnapshot) {
					snapshot.Actions[0].Status = actionmodel.Unavailable
					snapshot.Actions[0].StatusReason = "fixture configuration is unavailable"
				})
			})
			ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
			logs := fakes.NewProgressLogs(logErr)

			first, err := fixture.Manager.Spawn(action.WithLogs(ctx, logs), requestID, fixtures.Reference)

			if err != nil || len(first.Actions) != 1 || first.Actions[0].Status != actionmodel.Unavailable {
				t.Fatalf("progress changed the launch outcome or claimed ready actions: %+v, %v", first, err)
			}
			containerID := dockerDetails(t, first).ContainerID
			assertDockerProgress(t, logs.Messages(), []string{"Package version selection", "Docker preparation", "Docker launch reconciliation", "Docker registration observation"}, false,
				requestID, first.AgentID, containerID)
			before := journal.Checkpoints()
			if before["launch-intent"] == nil || before["package-selection"] == nil {
				t.Fatal("progress completion preceded a saved launch intent")
			}
			replayed := fakes.NewProgressLogs(logErr)

			second, err := fixture.Manager.Spawn(action.WithLogs(ctx, replayed), requestID, fixtures.Reference)

			if err != nil || second.AgentID != first.AgentID || dockerDetails(t, second).ContainerID != containerID {
				t.Fatalf("progress changed replay identity: %+v, %v", second, err)
			}
			assertDockerProgress(t, replayed.Messages(), []string{"Package version selection", "Docker launch reconciliation", "Docker registration observation"}, false,
				requestID, first.AgentID, containerID)
			for name, data := range before {
				if !bytes.Equal(journal.Checkpoints()[name], data) {
					t.Fatalf("progress replay changed checkpoint %s", name)
				}
			}
			if creates, starts, _ := fixture.Engine.Counts(); creates != 1 || starts != 1 {
				t.Fatalf("progress retried Docker effects: creates=%d starts=%d", creates, starts)
			}
		})
	}
}

func TestDockerProgressPreparationFailureDoesNotCreateLaunchIntent(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	fixture.Engine.SetPullError(errors.New("private-daemon-diagnostic " + fixtures.DockerImage))
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	logs := fakes.NewProgressLogs(errors.New("private-progress-backend-diagnostic"))

	_, err := fixture.Manager.Spawn(action.WithLogs(ctx, logs), requestID, fixtures.Reference)

	if err == nil {
		t.Fatal("log publication failure hid failed Docker preparation")
	}
	assertDockerProgress(t, logs.Messages(), []string{"Package version selection", "Docker preparation"}, true, requestID)
	if _, found := journal.Checkpoints()["launch-intent"]; found {
		t.Fatal("failed preparation saved an intent to launch")
	}
	if creates, starts, _ := fixture.Engine.Counts(); creates != 0 || starts != 0 {
		t.Fatal("failed preparation created or started a container")
	}
}

func TestDockerProgressReconcilesLostRepliesWithoutRepeatingEffects(t *testing.T) {
	for _, phase := range []string{"create", "start"} {
		t.Run(phase, func(t *testing.T) {
			fixture := fixtures.NewDockerManager(t)
			failure := errors.New("private-daemon-diagnostic " + fixture.Config.StorageURL)
			if phase == "create" {
				fixture.Engine.SetCreateError(failure)
			} else {
				fixture.Engine.SetStartError(failure)
			}
			ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
			logs := fakes.NewProgressLogs(errors.New("private-progress-backend-diagnostic"))

			_, err := fixture.Manager.Spawn(action.WithLogs(ctx, logs), requestID, fixtures.Reference)

			if err == nil || strings.Contains(err.Error(), "private-progress-backend-diagnostic") {
				t.Fatal("progress publication replaced an uncertain Docker outcome")
			}
			assertDockerProgress(t, logs.Messages(), []string{"Package version selection", "Docker preparation", "Docker launch reconciliation"}, true, requestID)
			intent := journal.Checkpoints()["launch-intent"]
			if intent == nil {
				t.Fatal("uncertain Docker outcome lost its durable intent")
			}
			replayed := fakes.NewProgressLogs(errors.New("private-progress-backend-diagnostic"))

			result, err := fixture.Manager.Spawn(action.WithLogs(ctx, replayed), requestID, fixtures.Reference)

			if err != nil || dockerDetails(t, result).ContainerID == "" {
				t.Fatalf("retry did not reconcile accepted %s: %v", phase, err)
			}
			assertDockerProgress(t, replayed.Messages(), []string{"Package version selection", "Docker launch reconciliation", "Docker registration observation"}, false,
				requestID, result.AgentID, dockerDetails(t, result).ContainerID)
			if !bytes.Equal(journal.Checkpoints()["launch-intent"], intent) {
				t.Fatal("progress replay replaced uncertain launch ownership")
			}
			if creates, starts, _ := fixture.Engine.Counts(); creates != 1 || starts != 1 {
				t.Fatalf("progress retried uncertain Docker effects: creates=%d starts=%d", creates, starts)
			}
		})
	}
}

func TestDockerProgressDoesNotRecreateAnUncertainMissingContainer(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	fixture.Engine.SetCreateError(errors.New("private-daemon-diagnostic"))
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	logs := fakes.NewProgressLogs(nil)
	if _, err := fixture.Manager.Spawn(action.WithLogs(ctx, logs), requestID, fixtures.Reference); err == nil {
		t.Fatal("lost create reply returned success")
	}
	intent := journal.Checkpoints()["launch-intent"]
	fixture.Engine.ForgetAll()
	replayed := fakes.NewProgressLogs(errors.New("private-progress-backend-diagnostic"))

	_, err := fixture.Manager.Spawn(action.WithLogs(ctx, replayed), requestID, fixtures.Reference)

	if err == nil || !bytes.Equal(journal.Checkpoints()["launch-intent"], intent) {
		t.Fatal("progress changed an uncertain missing-container intent")
	}
	assertDockerProgress(t, replayed.Messages(), []string{"Package version selection", "Docker launch reconciliation"}, true, requestID)
	if creates, starts, _ := fixture.Engine.Counts(); creates != 1 || starts != 0 {
		t.Fatalf("progress authorized a replacement launch: creates=%d starts=%d", creates, starts)
	}
}

func TestDockerProgressObservesRegistrationWhileCancellationRetainsOwnership(t *testing.T) {
	fixture := fixtures.NewDockerManager(t)
	fixture.Engine.SetOnStart(nil)
	executionCtx, requestID, journal := fixtures.SpawnCheckpoints(t)
	logs := fakes.NewProgressLogs(nil)
	ctx, cancel := context.WithCancel(action.WithLogs(executionCtx, logs))
	finished := make(chan error, 1)
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Error("Docker progress wait did not stop during cleanup")
			}
		}
	})
	go func() {
		_, err := fixture.Manager.Spawn(ctx, requestID, fixtures.Reference)
		finished <- err
	}()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
waiting:
	for {
		select {
		case message := <-logs.Published():
			if message == "Docker registration observation started" {
				break waiting
			}
		case err := <-finished:
			joined = true
			t.Fatalf("Docker spawn returned before observing registration: %v", err)
		case <-deadline.C:
			t.Fatal("running Docker registration wait did not report progress")
		}
	}
	cancel()
	select {
	case err := <-finished:
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("progress changed cancelled registration wait: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Docker cancellation did not join progress publication")
	}

	messages := logs.Messages()
	if len(messages) != 7 || messages[len(messages)-1] != "Docker registration observation started" {
		t.Fatalf("cancelled wait falsely reported completion: %v", messages)
	}
	if journal.Checkpoints()["launch-intent"] == nil {
		t.Fatal("cancellation removed child ownership")
	}
	spec := <-fixture.Engine.Created()
	container, err := fixture.Engine.Inspect(t.Context(), spec.Name)
	if err != nil || container.Status != "running" {
		t.Fatalf("cancelled wait changed its running child: %+v, %v", container, err)
	}
	if err := fixtures.PublishDockerRegistration(t.Context(), fixture.Bus, container, nil); err != nil {
		t.Fatal(err)
	}
	replayed := fakes.NewProgressLogs(errors.New("private-progress-backend-diagnostic"))

	result, err := fixture.Manager.Spawn(action.WithLogs(executionCtx, replayed), requestID, fixtures.Reference)

	if err != nil || dockerDetails(t, result).ContainerID != container.ID {
		t.Fatalf("progress retry missed retained late registration: %+v, %v", result, err)
	}
	assertDockerProgress(t, replayed.Messages(), []string{"Package version selection", "Docker launch reconciliation", "Docker registration observation"}, false,
		requestID, result.AgentID, container.ID)
	if creates, starts, _ := fixture.Engine.Counts(); creates != 1 || starts != 1 {
		t.Fatalf("cancelled progress wait repeated a launch: creates=%d starts=%d", creates, starts)
	}
}

func assertDockerProgress(t *testing.T, messages, stages []string, failed bool, privateValues ...string) {
	t.Helper()
	want := make([]string, 0, len(stages)*2)
	for index, stage := range stages {
		outcome := " completed"
		if failed && index == len(stages)-1 {
			outcome = " ended with error"
		}
		want = append(want, stage+" started", stage+outcome)
	}
	if !slices.Equal(messages, want) {
		t.Fatalf("unexpected Docker stage order: got %v, want %v", messages, want)
	}
	privateValues = append(privateValues, fixtures.Reference, fixtures.DockerImage, "amqp://fixture/", "s3://fixture",
		"private-storage-sentinel", "private-daemon-diagnostic", "private-progress-backend-diagnostic", "all actions ready")
	for _, message := range messages {
		for _, value := range privateValues {
			if strings.Contains(message, value) {
				t.Fatalf("progress exposed private state or overstated readiness: %q", message)
			}
		}
	}
}
