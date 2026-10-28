package executions_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/domain/executions"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/executions"
)

func TestCheckpointValidationAndBounds(t *testing.T) {
	exactSize := json.RawMessage(`{"value":"` + strings.Repeat("x", executions.MaxCheckpointSize-len(`{"value":""}`)) + `"}`)
	for _, tc := range []struct {
		name string
		data json.RawMessage
	}{
		{"", json.RawMessage(`{}`)},
		{"Invalid", json.RawMessage(`{}`)},
		{"with.dot", json.RawMessage(`{}`)},
		{"$operator", json.RawMessage(`{}`)},
		{strings.Repeat("a", executions.MaxCheckpointNameLength+1), json.RawMessage(`{}`)},
		{"empty", nil},
		{"null", json.RawMessage(`null`)},
		{"array", json.RawMessage(`[]`)},
		{"trailing", json.RawMessage(`{} {}`)},
		{"oversized", append(bytes.Clone(exactSize), ' ')},
		{"escaped-size", json.RawMessage(`{"value":"` + strings.Repeat("<>&\u2028\u2029", 1000) + `"}`)},
		{"encoding", []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := fixtures.Execution(t)
			if _, err := record.RecordCheckpoint(tc.name, tc.data); err == nil || len(record.Checkpoints) != 0 {
				t.Fatalf("invalid checkpoint changed execution: error=%v", err)
			}
			record.Checkpoints = map[string]json.RawMessage{tc.name: tc.data}
			if err := record.Validate(); err == nil {
				t.Fatal("invalid stored checkpoint was accepted")
			}
		})
	}

	record := fixtures.Execution(t)
	maxName := strings.Repeat("a", executions.MaxCheckpointNameLength)
	if added, err := record.RecordCheckpoint(maxName, exactSize); err != nil || !added {
		t.Fatalf("checkpoint at limits: added=%t error=%v", added, err)
	}
	for i := 1; i < executions.MaxCheckpoints; i++ {
		if _, err := record.RecordCheckpoint(fmt.Sprintf("step-%d", i), json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if added, err := record.RecordCheckpoint("overflow", json.RawMessage(`{}`)); err == nil || added {
		t.Fatalf("checkpoint count exceeded: added=%t error=%v", added, err)
	}
	if added, err := record.RecordCheckpoint(maxName, exactSize); err != nil || added {
		t.Fatalf("full execution rejected existing checkpoint: added=%t error=%v", added, err)
	}
	record.Checkpoints["overflow"] = json.RawMessage(`{}`)
	if err := record.Validate(); err == nil {
		t.Fatal("oversized stored checkpoint collection was accepted")
	}
}
