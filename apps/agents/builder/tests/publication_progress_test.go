package builder_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
)

func TestPublishAgentProgressPreservesCheckpointedPublication(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		name := "logs accepted"
		var logError error
		if unavailable {
			name, logError = "log transport failed", errors.New("log transport failed")
		}
		t.Run(name, func(t *testing.T) {
			f := fixtures.NewPublication(t, "", "")
			logs := fakes.NewProgressLogs(logError)
			f.Ctx = action.WithLogs(f.Ctx, logs)

			first, err := f.Run(t)

			if err != nil || len(first.Output) == 0 || len(first.Artifacts) != 0 || len(first.NextActions) != 0 {
				t.Fatalf("progress changed publication result: %+v %v", first, err)
			}
			assertPublicationProgress(t, logs, "Source publication request handling", "completed", fixtures.PublicationToken, f.Git.Bare, f.Input.Repository)
			checkpoints := f.Checkpoints.Snapshot()
			if len(checkpoints) != 2 || checkpoints["publication-intent"] == nil || checkpoints["publication-repository"] == nil {
				t.Fatalf("progress replaced publication facts: %+v", checkpoints)
			}

			second, err := f.Run(t)

			if err != nil || !bytes.Equal(first.Output, second.Output) || !reflect.DeepEqual(checkpoints, f.Checkpoints.Snapshot()) {
				t.Fatalf("logging changed immutable publication replay: %s %v", second.Output, err)
			}
			if f.HTTP.CreateCount() != 1 || publicationCalls(t, f, "push") != 1 {
				t.Fatal("publication progress triggered another repository or push")
			}
		})
	}
}

func TestReleaseAgentProgressPreservesPendingAndUncertainDispatch(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		for _, failedLogs := range []bool{false, true} {
			name := "pending"
			if uncertain {
				name = "uncertain"
			}
			if failedLogs {
				name += "/log transport failed"
			}
			t.Run(name, func(t *testing.T) {
				f := fixtures.NewRelease(t)
				var logError error
				if failedLogs {
					logError = errors.New("logging failure containing " + fixtures.PublicationToken)
				}
				logs := fakes.NewProgressLogs(logError)
				f.Ctx = action.WithLogs(f.Ctx, logs)
				if uncertain {
					f.HTTP.Change(func(state *fakes.ReleaseState) { state.LostDispatchAck, state.HideRun = true, true })
				}

				first, output, err := f.Execute(t)

				status, proposals, facts := "pending", 1, 3
				if uncertain {
					status, proposals, facts = "uncertain", 0, 2
				}
				if err != nil || output.Status != status || output.Package != "" || output.ReleaseURL != "" ||
					len(first.NextActions) != proposals || len(first.Artifacts) != 0 || f.HTTP.DispatchCount() != 1 {
					t.Fatalf("progress claimed a completed release or changed dispatch: %+v %v", output, err)
				}
				assertPublicationProgress(t, logs, "Release request handling", "completed", fixtures.PublicationToken, f.Input.Repository, f.Input.CommitSHA)
				checkpoints := f.Checkpoints.Snapshot()
				if len(checkpoints) != facts || checkpoints["release-intent"] == nil || string(checkpoints["release-dispatch-attempt"]) != `{"requested":true}` {
					t.Fatalf("progress changed immutable dispatch facts: %+v", checkpoints)
				}

				second, retried, err := f.Execute(t)

				if err != nil || !bytes.Equal(first.Output, second.Output) || retried.Status != status ||
					!reflect.DeepEqual(checkpoints, f.Checkpoints.Snapshot()) || f.HTTP.DispatchCount() != 1 {
					t.Fatalf("progress authorized a repeated release dispatch: %+v %v", retried, err)
				}
			})
		}
	}
}

func TestPublicationProgressPreservesWorkErrorsAndRedactsDiagnostics(t *testing.T) {
	t.Run("publish", func(t *testing.T) {
		f := fixtures.NewPublication(t, "", "")
		logs := fakes.NewProgressLogs(errors.New("logger failed with " + fixtures.PublicationToken))
		f.Ctx = action.WithLogs(f.Ctx, logs)
		want := errors.New("checkpoint failure: " + fixtures.PublicationToken + " " + f.Git.Bare + " " + f.Input.Repository)
		f.Checkpoints.SetAfterSave(func(name string) error {
			if name == "publication-intent" {
				return want
			}
			return nil
		})

		result, err := f.Run(t)

		if err != want || len(result.Output) != 0 || f.HTTP.RequestCount() != 0 || publicationCalls(t, f, "push") != 0 {
			t.Fatalf("logging changed a failed reservation or allowed publication: %+v %v", result, err)
		}
		assertPublicationProgress(t, logs, "Source publication request handling", "ended with error", fixtures.PublicationToken, f.Git.Bare, f.Input.Repository)
	})
	t.Run("release", func(t *testing.T) {
		f := fixtures.NewRelease(t)
		logs := fakes.NewProgressLogs(errors.New("logger failed with " + fixtures.PublicationToken))
		f.Ctx = action.WithLogs(f.Ctx, logs)
		privatePath := t.TempDir()
		want := errors.New("checkpoint failure: " + fixtures.PublicationToken + " " + privatePath + " " + f.Input.Repository)
		f.Checkpoints.SetAfterSave(func(name string) error {
			if name == "release-intent" {
				return want
			}
			return nil
		})

		result, _, err := f.Execute(t)

		if err != want || len(result.Output) != 0 || f.HTTP.DispatchCount() != 0 || len(f.Checkpoints.Snapshot()) != 1 {
			t.Fatalf("logging changed a failed release reservation or allowed dispatch: %+v %v", result, err)
		}
		assertPublicationProgress(t, logs, "Release request handling", "ended with error", fixtures.PublicationToken, privatePath, f.Input.Repository)
	})
}

func TestPublicationProgressStartsAfterValidationAndReadiness(t *testing.T) {
	for _, gate := range []string{"invalid input", "unavailable action"} {
		t.Run("publish/"+gate, func(t *testing.T) {
			f := fixtures.NewPublication(t, "", "")
			logs := fakes.NewProgressLogs(nil)
			f.Ctx = action.WithLogs(f.Ctx, logs)
			if gate == "invalid input" {
				f.Input.Repository = "https://invalid.example/" + fixtures.PublicationToken
			} else {
				f.Action.SetStatus(action.Unavailable)
			}

			_, err := f.Run(t)

			if err == nil || len(logs.Messages()) != 0 || f.HTTP.RequestCount() != 0 || len(f.Checkpoints.Snapshot()) != 0 {
				t.Fatalf("publication progress bypassed its gate: %v", err)
			}
		})
		t.Run("release/"+gate, func(t *testing.T) {
			f := fixtures.NewRelease(t)
			logs := fakes.NewProgressLogs(nil)
			f.Ctx = action.WithLogs(f.Ctx, logs)
			if gate == "invalid input" {
				f.Input.Repository = "https://invalid.example/" + fixtures.PublicationToken
			} else {
				f.Action.SetStatus(action.Unavailable)
			}

			_, _, err := f.Execute(t)

			if err == nil || len(logs.Messages()) != 0 || f.HTTP.RequestCount() != 0 || len(f.Checkpoints.Snapshot()) != 0 {
				t.Fatalf("release progress bypassed its gate: %v", err)
			}
		})
	}
}

func assertPublicationProgress(t *testing.T, logs *fakes.ProgressLogs, stage, ending string, private ...string) {
	t.Helper()
	messages := logs.Messages()
	if len(messages) < 2 || messages[0] != stage+" started" || messages[len(messages)-1] != stage+" "+ending {
		t.Fatalf("progress did not describe the request phase: %+v", messages)
	}
	for _, message := range messages {
		if message != stage+" started" && message != stage+" "+ending && !strings.HasPrefix(message, stage+" still running (elapsed ") {
			t.Fatalf("progress claimed an unrelated outcome: %s", message)
		}
		for _, value := range private {
			if strings.Contains(message, value) {
				t.Fatal("progress exposed private diagnostics or input")
			}
		}
	}
}
