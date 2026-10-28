package fixtures

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
)

func ExecutionLog(index int) executions.Log {
	return executions.Log{
		ID: fmt.Sprintf("11111111-1111-4111-8111-%012x", index), AgentID: "builder-agent-12345678",
		CommandID: "22222222-2222-4222-8222-222222222222", ActionName: "build-agent",
		Work:    messages.WorkContext{OperationID: "33333333-3333-4333-8333-333333333333", TaskID: "44444444-4444-4444-8444-444444444444", StepID: "build"},
		Message: "  Compiling packages\nTests are next ✓  ", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("fixture", 3*60*60)),
	}
}

func ExecutionLogJSON(t *testing.T, logs ...executions.Log) []byte {
	t.Helper()
	data, err := json.Marshal(logs)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func ExecutionLogPath(root, logID string) string {
	return filepath.Join(root, "execution_logs", logID+".json")
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
