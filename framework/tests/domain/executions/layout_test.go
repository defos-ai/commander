package executions_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNewExecutionUsesAgentAndCommandWithoutControllerMetadata(t *testing.T) {
	// Arrange
	root, request := t.TempDir(), command(t)
	// Agent types may contain the word controller.
	request.AgentID = "controller-agent-a1b2c3d4"
	store := openStore(t, root)

	// Act
	record, claimed, claimErr := store.Claim(t.Context(), request)
	reopened, readErr := openStore(t, root).Get(t.Context(), request.AgentID, request.ID)
	data, fileErr := os.ReadFile(filepath.Join(root, "executions", request.AgentID, request.ID+".json"))

	// Assert
	if claimErr != nil || !claimed || readErr != nil || !reflect.DeepEqual(record, reopened) {
		t.Fatalf("claim=%v claimed=%v read=%v record=%+v reopened=%+v", claimErr, claimed, readErr, record, reopened)
	}
	if fileErr != nil || strings.Contains(string(data), `"controller_type"`) || strings.Contains(string(data), `"controller_id"`) {
		t.Fatalf("unexpected execution layout or metadata: %s, %v", data, fileErr)
	}
}

func TestExecutionRejectsUnexpectedControllerMetadata(t *testing.T) {
	for _, field := range []string{"controller_type", "controller_id"} {
		t.Run(field, func(t *testing.T) {
			// Arrange: add a retired controller field to a current execution.
			root, request := t.TempDir(), command(t)
			store := openStore(t, root)
			if _, claimed, err := store.Claim(t.Context(), request); err != nil || !claimed {
				t.Fatalf("initial claim: claimed=%v error=%v", claimed, err)
			}
			path := filepath.Join(root, "executions", request.AgentID, request.ID+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var records []map[string]json.RawMessage
			if err := json.Unmarshal(data, &records); err != nil || len(records) != 1 {
				t.Fatalf("stored execution = %s, %v", data, err)
			}
			records[0][field] = json.RawMessage(`"default"`)
			data, err = json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}

			// Act
			_, getErr := store.Get(t.Context(), request.AgentID, request.ID)
			_, claimed, claimErr := store.Claim(t.Context(), request)
			retained, readErr := os.ReadFile(path)

			// Assert: neither reading nor claiming silently accepts or rewrites
			// execution records containing unsupported metadata.
			if getErr == nil || claimErr == nil || claimed {
				t.Fatalf("controller metadata accepted: get=%v claim=%v claimed=%v", getErr, claimErr, claimed)
			}
			if readErr != nil || !bytes.Equal(retained, data) {
				t.Fatalf("rejected execution changed: got=%s error=%v", retained, readErr)
			}
		})
	}
}
