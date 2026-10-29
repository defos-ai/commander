package fixtures

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
)

type executionLogRecord struct {
	Sequence uint64         `json:"sequence"`
	Log      executions.Log `json:"log"`
}

func ExecutionLog(index int) executions.Log {
	return executions.Log{
		ID: fmt.Sprintf("11111111-1111-4111-8111-%012x", index), AgentID: "builder-agent-12345678",
		CommandID: "22222222-2222-4222-8222-222222222222", ActionName: "build-agent",
		Work:    messages.WorkContext{OperationID: "33333333-3333-4333-8333-333333333333", TaskID: "44444444-4444-4444-8444-444444444444"},
		Message: "  Compiling packages\nTests are next ✓  ", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("fixture", 3*60*60)),
	}
}

func ExecutionLogJSON(t *testing.T, logs ...executions.Log) []byte {
	t.Helper()
	records := make([]executionLogRecord, len(logs))
	for index, log := range logs {
		records[index].Sequence, records[index].Log = uint64(index+1), log
	}
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func ExecutionLogPath(root, logID string) string {
	return filepath.Join(root, "execution_logs", logID+".json")
}

func ExecutionLogIndexPath(root string) string {
	return filepath.Join(root, "execution_logs", "index.json")
}

func ExecutionLogPositionPath(root string, sequence uint64) string {
	return filepath.Join(root, "execution_logs", "positions", fmt.Sprintf("%020d.json", sequence))
}

func StageExecutionLogAppend(t *testing.T, root string, sequence uint64, log executions.Log, phase string) {
	t.Helper()
	record := executionLogRecord{Sequence: sequence, Log: log}
	writeExecutionLogFile(t, ExecutionLogIndexPath(root), []struct {
		Committed uint64             `json:"committed"`
		Pending   executionLogRecord `json:"pending"`
	}{{Committed: sequence - 1, Pending: record}})
	if phase == "intent" {
		return
	}
	writeExecutionLogFile(t, ExecutionLogPath(root, log.ID), []executionLogRecord{record})
	if phase == "body" {
		return
	}
	if phase != "position" {
		t.Fatalf("unknown log append phase: %s", phase)
	}
	writeExecutionLogFile(t, ExecutionLogPositionPath(root, sequence), []struct {
		Sequence uint64 `json:"sequence"`
		LogID    string `json:"log_id"`
	}{{Sequence: sequence, LogID: log.ID}})
}

func BlockExecutionLogPosition(t *testing.T, root string, sequence uint64) func() {
	t.Helper()
	path := ExecutionLogPositionPath(root, sequence)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	removed := false
	restore := func() {
		t.Helper()
		if removed {
			return
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Error(err)
			return
		}
		removed = true
	}
	t.Cleanup(restore)
	return restore
}

func CorruptExecutionLogIndex(t *testing.T, root string, log executions.Log, corruption string) {
	t.Helper()
	path := ExecutionLogIndexPath(root)
	var data []byte
	switch corruption {
	case "missing head":
	case "missing head field":
		data = []byte(`[{}]`)
	case "head gap":
		data = []byte(`[{"committed":2}]`)
	case "missing position":
		path = ExecutionLogPositionPath(root, 1)
	case "position identity":
		path = ExecutionLogPositionPath(root, 1)
		data = []byte(`[{"sequence":1,"log_id":"11111111-1111-4111-8111-111111111112"}]`)
	case "position sequence":
		path = ExecutionLogPositionPath(root, 1)
		data = []byte(`[{"sequence":2,"log_id":"11111111-1111-4111-8111-000000000001"}]`)
	case "record sequence":
		path = ExecutionLogPath(root, log.ID)
		data = bytes.Replace(ExecutionLogJSON(t, log), []byte(`"sequence":1`), []byte(`"sequence":2`), 1)
	default:
		t.Fatalf("unknown execution log corruption: %s", corruption)
	}
	if data == nil {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func ExecutionLogIndexStaging(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Dir(ExecutionLogIndexPath(root))
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".index.json-interrupted.tmp"), []byte(`partial pending intent`), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeExecutionLogFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func WriteExecutionLogState(t *testing.T, root, logID string, data []byte) string {
	t.Helper()
	path := ExecutionLogPath(root, logID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func DenyExecutionLogWrites(t *testing.T, directory string) func() {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions and an unprivileged user")
	}
	restore := func() {
		t.Helper()
		if err := os.Chmod(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(restore)
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	return restore
}
