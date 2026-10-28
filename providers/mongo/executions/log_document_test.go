package executions

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestLogDocumentPreservesJSONPayload(t *testing.T) {
	value := executions.Log{
		ID: "10000000-0000-4000-8000-000000000001", AgentID: "builder-agent-01234567",
		CommandID: "20000000-0000-4000-8000-000000000001", ActionName: "build-agent",
		Work: messages.WorkContext{OperationID: "30000000-0000-4000-8000-000000000001",
			TaskID: "40000000-0000-4000-8000-000000000001", StepID: "build"},
		Message:   "  Working <module>\nготово 👋\t",
		CreatedAt: time.Date(2026, 10, 29, 12, 0, 0, 123456789, time.FixedZone("test", 3*60*60)),
	}
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := bson.Marshal(logDocument{ID: value.ID, Data: payload})
	if err != nil {
		t.Fatal(err)
	}
	var document logDocument
	if err := bson.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	saved, err := document.decode()

	if err != nil || !saved.Equal(value) || saved.Message != value.Message || saved.CreatedAt.Nanosecond() != 123456789 {
		t.Fatalf("BSON/JSON roundtrip changed the execution log: %+v, %v", saved, err)
	}
	if !bytes.Equal(document.Data, payload) || bson.Raw(encoded).Lookup("data").Type != bson.TypeBinary {
		t.Fatal("BSON did not preserve the JSON binary payload")
	}
	for _, test := range []struct {
		name string
		doc  logDocument
	}{
		{"malformed", logDocument{ID: value.ID, Data: json.RawMessage(`{`)}},
		{"invalid record", logDocument{ID: value.ID, Data: json.RawMessage(`{}`)}},
		{"wrong ID", logDocument{ID: "10000000-0000-4000-8000-000000000002", Data: payload}},
		{"unknown field", logDocument{ID: value.ID, Data: append([]byte(`{"unexpected":true,`), payload[1:]...)}},
		{"trailing JSON", logDocument{ID: value.ID, Data: append(bytes.Clone(payload), []byte(`{}`)...)}},
		{"invalid UTF-8", logDocument{ID: value.ID, Data: bytes.Replace(payload, []byte("Working"), []byte{0xff}, 1)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.doc.decode()

			if err == nil {
				t.Fatal("invalid persisted execution log was accepted")
			}
		})
	}
}
