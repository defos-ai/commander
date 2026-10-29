package providers_test

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestExecutionLogsRecoverPendingAppendBeforeAdvancingTheCursor(t *testing.T) {
	for _, phase := range []string{"intent", "body", "position"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			store := fixtures.CheckpointStore(t, root)
			first, pending, next := fixtures.ExecutionLog(1), fixtures.ExecutionLog(2), fixtures.ExecutionLog(3)
			if err := store.AppendLog(t.Context(), first); err != nil {
				t.Fatal(err)
			}
			fixtures.StageExecutionLogAppend(t, root, 2, pending, phase)
			before, err := os.ReadFile(fixtures.ExecutionLogIndexPath(root))
			if err != nil {
				t.Fatal(err)
			}
			reopened := fixtures.CheckpointStore(t, root)

			_, pendingErr := reopened.GetLog(t.Context(), pending.ID)
			_, cursorErr := reopened.ListLogs(t.Context(), executions.LogQuery{AfterLogID: pending.ID, Limit: 10})
			_, descendingCursorErr := reopened.ListLogs(t.Context(), executions.LogQuery{Order: executions.LogOrderDescending, AfterLogID: pending.ID, Limit: 10})
			page, err := reopened.ListLogs(t.Context(), executions.LogQuery{Limit: 10})
			descending, descendingErr := reopened.ListLogs(t.Context(), executions.LogQuery{Order: executions.LogOrderDescending, Limit: 10})
			after, readErr := os.ReadFile(fixtures.ExecutionLogIndexPath(root))

			if pendingErr == nil || !errors.Is(cursorErr, executions.ErrUnknownLog) || !errors.Is(descendingCursorErr, executions.ErrUnknownLog) || err != nil || readErr != nil || len(page) != 1 ||
				!page[0].Equal(first) || !bytes.Equal(before, after) {
				t.Fatalf("read exposed or committed pending work: pending=%v cursor=%v page=%+v error=%v metadata=%v", pendingErr, cursorErr, page, err, readErr)
			}
			assertExecutionLogs(t, descending, descendingErr, first)
			if phase == "intent" {
				if _, err := os.Stat(fixtures.ExecutionLogPath(root, pending.ID)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("reading an intent created its body: %v", err)
				}
			}

			if err := reopened.AppendLog(t.Context(), next); err != nil {
				t.Fatal(err)
			}
			if err := reopened.AppendLog(t.Context(), pending); err != nil {
				t.Fatalf("recovered log was not idempotent: %v", err)
			}
			recovered, err := reopened.ListLogs(t.Context(), executions.LogQuery{AfterLogID: first.ID, Limit: 10})

			if err != nil || len(recovered) != 2 || !recovered[0].Equal(pending) ||
				!recovered[1].Equal(next) {
				t.Fatalf("recovery lost, duplicated or reordered an append: %+v, %v", recovered, err)
			}
		})
	}
}

func TestExecutionLogsRecoverAfterPositionPublicationFails(t *testing.T) {
	root := t.TempDir()
	store := fixtures.CheckpointStore(t, root)
	first, pending, next := fixtures.ExecutionLog(1), fixtures.ExecutionLog(2), fixtures.ExecutionLog(3)
	if err := store.AppendLog(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	restore := fixtures.BlockExecutionLogPosition(t, root, 2)

	appendErr := store.AppendLog(t.Context(), pending)
	_, readErr := store.GetLog(t.Context(), pending.ID)
	page, pageErr := store.ListLogs(t.Context(), executions.LogQuery{Limit: 10})

	if appendErr == nil || readErr == nil || pageErr != nil || len(page) != 1 || !page[0].Equal(first) {
		t.Fatalf("failed publication exposed a partial append: append=%v read=%v page=%+v error=%v", appendErr, readErr, page, pageErr)
	}
	restore()

	reopened := fixtures.CheckpointStore(t, root)
	if err := reopened.AppendLog(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	recovered, err := reopened.ListLogs(t.Context(), executions.LogQuery{AfterLogID: first.ID, Limit: 10})

	if err != nil || len(recovered) != 2 || !recovered[0].Equal(pending) ||
		!recovered[1].Equal(next) {
		t.Fatalf("failed append was abandoned or its position reused: %+v, %v", recovered, err)
	}
}

func TestExecutionLogsRejectMissingAndInconsistentIndexState(t *testing.T) {
	for _, corruption := range []string{"missing head", "missing head field", "missing position", "position identity", "position sequence", "record sequence"} {
		t.Run(corruption, func(t *testing.T) {
			root := t.TempDir()
			store := fixtures.CheckpointStore(t, root)
			log := fixtures.ExecutionLog(1)
			if err := store.AppendLog(t.Context(), log); err != nil {
				t.Fatal(err)
			}
			fixtures.CorruptExecutionLogIndex(t, root, log, corruption)

			_, readErr := store.GetLog(t.Context(), log.ID)
			_, listErr := store.ListLogs(t.Context(), executions.LogQuery{Limit: 10})

			if readErr == nil || listErr == nil {
				t.Fatalf("incomplete indexed state was accepted: get=%v list=%v", readErr, listErr)
			}
		})
	}
}

func TestExecutionLogsIgnoreUncommittedInitialIndexStaging(t *testing.T) {
	root := t.TempDir()
	fixtures.ExecutionLogIndexStaging(t, root)
	store := fixtures.CheckpointStore(t, root)

	page, err := store.ListLogs(t.Context(), executions.LogQuery{Limit: 10})

	if err != nil || page == nil || len(page) != 0 {
		t.Fatalf("unpublished initialization bytes became committed history: %+v, %v", page, err)
	}
	if err := store.AppendLog(t.Context(), fixtures.ExecutionLog(1)); err != nil {
		t.Fatalf("unpublished initialization bytes blocked a later append: %v", err)
	}
}

func TestExecutionLogsRejectACommittedHeadWithoutItsMatchingTail(t *testing.T) {
	for _, tail := range []string{"head gap", "record sequence"} {
		t.Run(tail, func(t *testing.T) {
			root := t.TempDir()
			store := fixtures.CheckpointStore(t, root)
			first, next := fixtures.ExecutionLog(1), fixtures.ExecutionLog(2)
			if err := store.AppendLog(t.Context(), first); err != nil {
				t.Fatal(err)
			}
			fixtures.CorruptExecutionLogIndex(t, root, first, tail)

			_, getErr := store.GetLog(t.Context(), first.ID)
			_, listErr := store.ListLogs(t.Context(), executions.LogQuery{AfterLogID: first.ID, Limit: 10})
			appendErr := store.AppendLog(t.Context(), next)

			if getErr == nil || listErr == nil || appendErr == nil {
				t.Fatalf("invalid committed tail was bypassed: get=%v list=%v append=%v", getErr, listErr, appendErr)
			}
			if _, err := os.Stat(fixtures.ExecutionLogPath(root, next.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid head admitted another append: %v", err)
			}
		})
	}
}
