package agent_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

func TestProgressReportsPacedLivenessAndStopsWithTheStage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := fakes.NewProgressLogs(nil)
		ctx := action.WithLogs(t.Context(), logs)
		started := time.Now()
		runs := 0

		err := action.RunWithProgress(ctx, "Codex generation", func() error {
			runs++
			time.Sleep(65 * time.Second)
			return nil
		})

		want := []string{"Codex generation started", "Codex generation still running (elapsed 30s)",
			"Codex generation still running (elapsed 1m0s)", "Codex generation completed"}
		if err != nil || runs != 1 || !reflect.DeepEqual(logs.Messages(), want) {
			t.Fatalf("stage progress changed work or pacing: error=%v runs=%d messages=%v", err, runs, logs.Messages())
		}
		for index, elapsed := range []time.Duration{0, 30 * time.Second, 60 * time.Second, 65 * time.Second} {
			call := logs.Calls()[index]
			if !call.StartedAt.Equal(started.Add(elapsed)) || call.Deadline.Sub(call.StartedAt) != time.Second {
				t.Fatalf("progress publication lost its pacing or time bound: %+v", call)
			}
		}
		copy := logs.Messages()
		copy[0] = "changed"

		time.Sleep(time.Minute)

		if !reflect.DeepEqual(logs.Messages(), want) {
			t.Fatalf("stage retained a heartbeat worker or exposed mutable capture: %v", logs.Messages())
		}
	})
}

func TestProgressPreservesWorkErrorsWhenLoggingFails(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				providerError := errors.New("private log backend path and credentials")
				logs := fakes.NewProgressLogs(providerError)
				ctx := action.WithLogs(t.Context(), logs)
				var workError error
				if failure {
					workError = errors.New("untrusted tool output and credentials")
				}
				runs := 0

				err := action.RunWithProgress(ctx, "Go tests", func() error {
					runs++
					time.Sleep(31 * time.Second)
					return workError
				})

				ending := "Go tests completed"
				if failure {
					ending = "Go tests ended with error"
				}
				want := []string{"Go tests started", "Go tests still running (elapsed 30s)", ending}
				if err != workError || runs != 1 || !reflect.DeepEqual(logs.Messages(), want) || ctx.Err() != nil {
					t.Fatalf("logging failed the stage, changed its error, retried it or exposed diagnostics: error=%v runs=%d messages=%v", err, runs, logs.Messages())
				}
			})
		})
	}
}

func TestProgressSkipsHeartbeatsWhenLogsAreUnavailable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := fakes.NewProgressLogs(action.ErrLogsUnavailable)
		ctx := action.WithLogs(t.Context(), logs)
		workError := errors.New("work failed")
		runs := 0

		err := action.RunWithProgress(ctx, "Release request handling", func() error {
			runs++
			time.Sleep(95 * time.Second)
			return workError
		})

		if err != workError || runs != 1 || !reflect.DeepEqual(logs.Messages(), []string{"Release request handling started"}) {
			t.Fatalf("unavailable logs changed work or kept a reporter: error=%v runs=%d messages=%v", err, runs, logs.Messages())
		}
	})
}

func TestProgressBoundsBlockedPublicationsWithoutCancelingWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := fakes.NewProgressLogs(nil)
		logs.Block()
		ctx := action.WithLogs(t.Context(), logs)
		started := time.Now()
		workError := errors.New("work failed")
		runs := 0

		err := action.RunWithProgress(ctx, "Go build", func() error {
			runs++
			if ctx.Err() != nil {
				t.Errorf("progress canceled work: %v", ctx.Err())
			}
			return workError
		})

		if err != workError || runs != 1 || ctx.Err() != nil || time.Since(started) != 2*time.Second ||
			!reflect.DeepEqual(logs.Messages(), []string{"Go build started", "Go build ended with error"}) {
			t.Fatalf("blocked publications escaped their bounds or changed work: error=%v runs=%d elapsed=%s messages=%v", err, runs, time.Since(started), logs.Messages())
		}
		for _, call := range logs.Calls() {
			if !errors.Is(call.Error, context.DeadlineExceeded) || call.FinishedAt.Sub(call.StartedAt) != time.Second {
				t.Fatalf("publication exceeded its own context: %+v", call)
			}
		}
	})
}

func TestProgressCancelsAndJoinsAnInflightHeartbeatOnReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := fakes.NewProgressLogs(nil)
		logs.BlockHeartbeats()
		ctx := action.WithLogs(t.Context(), logs)
		started := time.Now()

		err := action.RunWithProgress(ctx, "Codex generation", func() error {
			time.Sleep(30*time.Second + 500*time.Millisecond)
			return nil
		})

		calls := logs.Calls()
		if err != nil || len(calls) != 3 || !errors.Is(calls[1].Error, context.Canceled) ||
			!calls[1].FinishedAt.Equal(started.Add(30*time.Second+500*time.Millisecond)) ||
			calls[2].StartedAt.Before(calls[1].FinishedAt) || calls[2].Message != "Codex generation completed" {
			t.Fatalf("stage did not cancel and join its active reporter before completion: error=%v calls=%+v", err, calls)
		}
	})
}

func TestProgressStopsOnCallerCancellationWhileWorkFinishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := fakes.NewProgressLogs(nil)
		logs.BlockHeartbeats()
		ctx, cancel := context.WithCancel(action.WithLogs(t.Context(), logs))
		defer cancel()
		cancellation := time.AfterFunc(30*time.Second+500*time.Millisecond, cancel)
		defer cancellation.Stop()
		started := time.Now()
		workError := errors.New("work's own outcome")

		err := action.RunWithProgress(ctx, "Codex generation", func() error {
			time.Sleep(95 * time.Second)
			return workError
		})

		calls := logs.Calls()
		if err != workError || len(calls) != 2 || !errors.Is(calls[1].Error, context.Canceled) ||
			!calls[1].FinishedAt.Equal(started.Add(30*time.Second+500*time.Millisecond)) || time.Since(started) != 95*time.Second {
			t.Fatalf("reporting outlived caller cancellation or changed work: error=%v calls=%+v elapsed=%s", err, calls, time.Since(started))
		}
	})
}

func TestProgressKeepsAnEarlierCallerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := fakes.NewProgressLogs(nil)
		logs.Block()
		ctx, cancel := context.WithTimeout(action.WithLogs(t.Context(), logs), 250*time.Millisecond)
		defer cancel()
		started := time.Now()
		workError := errors.New("work's own outcome")
		runs := 0

		err := action.RunWithProgress(ctx, "Go build", func() error {
			runs++
			return workError
		})

		calls := logs.Calls()
		if err != workError || runs != 1 || len(calls) != 1 || time.Since(started) != 250*time.Millisecond ||
			calls[0].Deadline.Sub(started) != 250*time.Millisecond || !errors.Is(calls[0].Error, context.DeadlineExceeded) {
			t.Fatalf("progress extended its caller's deadline or replaced work: error=%v runs=%d calls=%+v elapsed=%s", err, runs, calls, time.Since(started))
		}
	})
}

func TestProgressJoinsItsReporterWhenWorkPanics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := fakes.NewProgressLogs(nil)
		logs.BlockHeartbeats()
		ctx := action.WithLogs(t.Context(), logs)
		panicValue := errors.New("private tool output and credentials")
		var recovered any

		func() {
			defer func() { recovered = recover() }()
			_ = action.RunWithProgress(ctx, "Codex generation", func() error {
				time.Sleep(30*time.Second + 500*time.Millisecond)
				panic(panicValue)
			})
		}()

		want := []string{"Codex generation started", "Codex generation still running (elapsed 30s)", "Codex generation ended with error"}
		calls := logs.Calls()
		if recovered != panicValue || !reflect.DeepEqual(logs.Messages(), want) || !errors.Is(calls[1].Error, context.Canceled) {
			t.Fatalf("panic changed identity, escaped reporter cleanup or leaked its details: recovered=%v messages=%v calls=%+v", recovered, logs.Messages(), calls)
		}

		time.Sleep(time.Minute)

		if !reflect.DeepEqual(logs.Messages(), want) {
			t.Fatalf("panicking work retained its heartbeat worker: %v", logs.Messages())
		}
	})
}
