package providers_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	executionfs "github.com/defos-ai/commander/providers/fs/executions"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestExecutionLogsPreserveReplayAndExactText(t *testing.T) {
	for _, message := range []string{strings.Repeat("a", executions.MaxLogMessageSize), strings.Repeat("é", executions.MaxLogMessageSize/2)} {
		t.Run(fmt.Sprintf("%d UTF-8 runes", len([]rune(message))), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "database")
			store := fixtures.CheckpointStore(t, root)
			log := fixtures.ExecutionLog(1)
			log.Message = message

			if err := store.AppendLog(t.Context(), log); err != nil {
				t.Fatal(err)
			}
			reopened := fixtures.CheckpointStore(t, root)
			saved, err := reopened.GetLog(t.Context(), log.ID)

			if err != nil || !saved.Equal(log) || saved.CreatedAt.Format(time.RFC3339Nano) != log.CreatedAt.Format(time.RFC3339Nano) {
				t.Fatalf("log changed across reopen: %+v, %v", saved, err)
			}
			path := fixtures.ExecutionLogPath(root, log.ID)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			replay := log
			replay.CreatedAt = replay.CreatedAt.UTC()

			if err := reopened.AppendLog(t.Context(), replay); err != nil {
				t.Fatalf("same-instant replay was rejected: %v", err)
			}
			after, err := os.ReadFile(path)

			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("replay changed the original timestamp representation or text")
			}
		})
	}
}

func TestExecutionLogsRejectConflictingIdentityAndContent(t *testing.T) {
	root := t.TempDir()
	store := fixtures.CheckpointStore(t, root)
	original := fixtures.ExecutionLog(1)
	if err := store.AppendLog(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*executions.Log)
	}{
		{"agent", func(log *executions.Log) { log.AgentID = "builder-agent-87654321" }},
		{"command", func(log *executions.Log) { log.CommandID = fixtures.ExecutionLog(2).ID }},
		{"action", func(log *executions.Log) { log.ActionName = "publish-agent" }},
		{"operation", func(log *executions.Log) { log.Work.OperationID = fixtures.ExecutionLog(2).ID }},
		{"task", func(log *executions.Log) { log.Work.TaskID = fixtures.ExecutionLog(2).ID }},
		{"step", func(log *executions.Log) { log.Work.StepID = "publish" }},
		{"standalone scope", func(log *executions.Log) { log.Work = messages.WorkContext{} }},
		{"message", func(log *executions.Log) { log.Message += " changed" }},
		{"nanosecond", func(log *executions.Log) { log.CreatedAt = log.CreatedAt.Add(time.Nanosecond) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			conflict := original
			test.change(&conflict)

			err := store.AppendLog(t.Context(), conflict)
			saved, readErr := store.GetLog(t.Context(), original.ID)

			if !errors.Is(err, executions.ErrLogConflict) || readErr != nil || !saved.Equal(original) {
				t.Fatalf("conflicting log changed the saved observation: append=%v read=%v", err, readErr)
			}
		})
	}
}

func TestExecutionLogsSerializeIndependentWriters(t *testing.T) {
	root := t.TempDir()
	stores := []executions.ExecutionStore{fixtures.CheckpointStore(t, root), fixtures.CheckpointStore(t, root)}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	const writers = 12
	start := make(chan struct{})
	type outcome struct {
		log executions.Log
		err error
	}
	finished := make(chan outcome, writers)
	for index := range writers {
		log := fixtures.ExecutionLog(1)
		log.Message = fmt.Sprintf("Writer %d", index)
		go func() {
			<-start
			finished <- outcome{log, stores[index%len(stores)].AppendLog(ctx, log)}
		}()
	}

	close(start)
	var winner executions.Log
	winners, conflicts := 0, 0
	for range writers {
		result := <-finished
		switch {
		case result.err == nil:
			winner = result.log
			winners++
		case errors.Is(result.err, executions.ErrLogConflict):
			conflicts++
		default:
			t.Error(result.err)
		}
	}
	saved, err := stores[0].GetLog(ctx, fixtures.ExecutionLog(1).ID)

	if winners != 1 || conflicts != writers-1 || err != nil || !saved.Equal(winner) {
		t.Fatalf("concurrent writers lost the unique winner: winners=%d conflicts=%d read=%v", winners, conflicts, err)
	}
	for index := range writers {
		go func() { finished <- outcome{winner, stores[index%len(stores)].AppendLog(ctx, winner)} }()
	}
	for range writers {
		if result := <-finished; result.err != nil {
			t.Errorf("identical concurrent retry failed: %v", result.err)
		}
	}
}

func TestExecutionLogsValidateBeforeCreatingState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	store := fixtures.CheckpointStore(t, root)
	log := fixtures.ExecutionLog(1)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := executionfs.NewExecutionFsStore(" \t"); err == nil {
		t.Fatal("blank database root was accepted")
	}
	for _, ctx := range []context.Context{nil, canceled} {
		if err := store.AppendLog(ctx, log); err == nil || ctx != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("invalid context appended a log: %v", err)
		}
		if _, err := store.GetLog(ctx, log.ID); err == nil || ctx != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("invalid context read a log: %v", err)
		}
	}
	for _, change := range []func(*executions.Log){
		func(value *executions.Log) { value.ID = "../outside" },
		func(value *executions.Log) { value.CommandID = "" },
		func(value *executions.Log) { value.Message = "invalid\xff" },
		func(value *executions.Log) { value.Message = strings.Repeat("x", executions.MaxLogMessageSize+1) },
	} {
		invalid := log
		change(&invalid)
		if err := store.AppendLog(t.Context(), invalid); err == nil {
			t.Fatal("invalid log was appended")
		}
	}
	if _, err := store.GetLog(t.Context(), "../outside"); err == nil {
		t.Fatal("path-like log identity was accepted")
	}
	if _, err := store.GetLog(t.Context(), log.ID); !errors.Is(err, executions.ErrUnknownLog) {
		t.Fatalf("absent log: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid inputs or an absent read created state: %v", err)
	}
}

func TestExecutionLogsRejectMalformedSavedState(t *testing.T) {
	log := fixtures.ExecutionLog(1)
	encoded := fixtures.ExecutionLogJSON(t, log)
	invalidRecord := log
	invalidRecord.AgentID = "invalid"
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"non-array", []byte(`{}`)},
		{"null", []byte(`null`)},
		{"invalid JSON", []byte(`[`)},
		{"trailing data", append(bytes.Clone(encoded), []byte(` []`)...)},
		{"unknown field", bytes.Replace(encoded, []byte(`"message":`), []byte(`"unknown":true,"message":`), 1)},
		{"multiple records", fixtures.ExecutionLogJSON(t, log, fixtures.ExecutionLog(2))},
		{"different identity", fixtures.ExecutionLogJSON(t, fixtures.ExecutionLog(2))},
		{"invalid record", fixtures.ExecutionLogJSON(t, invalidRecord)},
		{"invalid UTF-8", bytes.Replace(encoded, []byte("Compiling"), []byte("Compiling\xff"), 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store := fixtures.CheckpointStore(t, root)
			path := fixtures.WriteExecutionLogState(t, root, log.ID, test.data)

			_, readErr := store.GetLog(t.Context(), log.ID)
			appendErr := store.AppendLog(t.Context(), log)
			after, err := os.ReadFile(path)

			if readErr == nil || errors.Is(readErr, executions.ErrUnknownLog) || appendErr == nil || err != nil || !bytes.Equal(after, test.data) {
				t.Fatalf("malformed saved state was exposed or overwritten: read=%v append=%v file=%v", readErr, appendErr, err)
			}
		})
	}
	root := t.TempDir()
	store := fixtures.CheckpointStore(t, root)
	fixtures.WriteExecutionLogState(t, root, log.ID, []byte(`[]`))
	if _, err := store.GetLog(t.Context(), log.ID); !errors.Is(err, executions.ErrUnknownLog) {
		t.Fatalf("empty collection did not mean unknown log: %v", err)
	}
	if err := store.AppendLog(t.Context(), log); err != nil {
		t.Fatalf("empty collection rejected its first log: %v", err)
	}
}

func TestExecutionLogsIsolateRecordsAndExecutionState(t *testing.T) {
	root := t.TempDir()
	store := fixtures.CheckpointStore(t, root)
	command, result := fixtures.CheckpointMessages(t)
	if _, _, err := store.Claim(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCheckpoint(t.Context(), command.AgentID, command.ID, "intent", []byte(`{"started":true}`)); err != nil {
		t.Fatal(err)
	}
	before, err := store.Get(t.Context(), command.AgentID, command.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixtures.WriteExecutionLogState(t, root, fixtures.ExecutionLog(1).ID, []byte(`corrupted unrelated history`))
	log := fixtures.ExecutionLog(2)
	log.AgentID, log.CommandID, log.Work, log.ActionName = command.AgentID, command.ID, command.Work, "inspect"

	if err := store.AppendLog(t.Context(), log); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetLog(t.Context(), log.ID)
	after, executionErr := store.Get(t.Context(), command.AgentID, command.ID)

	if err != nil || !saved.Equal(log) || executionErr != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("independent log storage touched execution state or unrelated history: log=%v execution=%v", err, executionErr)
	}
	if err := store.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	late := log
	late.ID = fixtures.ExecutionLog(3).ID
	late.Message = "Late progress delivery"
	if err := store.AppendLog(t.Context(), late); err != nil {
		t.Fatalf("completed execution rejected an independent observation: %v", err)
	}
}

func TestExecutionLogsRejectLinksAndRetriesPublication(t *testing.T) {
	for _, kind := range []string{"record symlink", "lock symlink", "record directory", "namespace symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			store := fixtures.CheckpointStore(t, root)
			log := fixtures.ExecutionLog(1)
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			path := fixtures.ExecutionLogPath(root, log.ID)
			if kind == "namespace symlink" {
				fixtures.Link(t, filepath.Dir(outside), filepath.Dir(path))
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "record directory":
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				case "lock symlink":
					fixtures.Link(t, outside, path+".lock")
				default:
					fixtures.Link(t, outside, path)
				}
			}

			err := store.AppendLog(t.Context(), log)
			data, readErr := os.ReadFile(outside)

			if err == nil || readErr != nil || string(data) != "unchanged" {
				t.Fatalf("unsafe log path was followed: append=%v outside=%v", err, readErr)
			}
			if kind != "lock symlink" {
				if _, err := store.GetLog(t.Context(), log.ID); err == nil || errors.Is(err, executions.ErrUnknownLog) {
					t.Fatalf("unsafe read path was followed or treated as absent: %v", err)
				}
			}
		})
	}
	t.Run("identical retry still publishes", func(t *testing.T) {
		root := t.TempDir()
		store := fixtures.CheckpointStore(t, root)
		log := fixtures.ExecutionLog(1)
		if err := store.AppendLog(t.Context(), log); err != nil {
			t.Fatal(err)
		}
		restore := fixtures.DenyExecutionLogWrites(t, filepath.Dir(fixtures.ExecutionLogPath(root, log.ID)))

		err := store.AppendLog(t.Context(), log)
		saved, readErr := store.GetLog(t.Context(), log.ID)

		if !errors.Is(err, os.ErrPermission) || readErr != nil || !saved.Equal(log) {
			t.Fatalf("retry skipped publication or lost saved data: append=%v read=%v", err, readErr)
		}
		restore()
		if err := store.AppendLog(t.Context(), log); err != nil {
			t.Fatalf("retry did not recover after restoring storage: %v", err)
		}
	})
}
