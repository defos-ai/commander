package comms_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/comms"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/executions"
)

func TestExecutionLogProtocolPreservesObservationAndRouting(t *testing.T) {
	for _, text := range []string{
		"  Compiling packages\nTests are next ✓  ",
		strings.Repeat("<", executions.MaxLogMessageSize),
		strings.Repeat("é", executions.MaxLogMessageSize/2),
	} {
		log := fixtures.Log()
		log.Message = text
		bus := &fakes.SecretBus{}

		event, err := controllerevents.NewExecutionLog(log)
		if err != nil {
			t.Fatal(err)
		}
		err = (controllerevents.Client{Bus: bus}).Publish(t.Context(), event)

		if err != nil {
			t.Fatal(err)
		}
		decoded, err := controllerevents.DecodeExecutionLog(event)
		if err != nil || !decoded.Equal(log) || event.ID != log.ID || event.CorrelationID != log.CommandID ||
			event.AgentID != log.AgentID || event.Work != log.Work || !event.CreatedAt.Equal(log.CreatedAt) {
			t.Fatalf("execution log lost its identity or text: %v", err)
		}
		if topics := bus.Topics(); len(topics) != 1 || topics[0] != messages.ControllerEvents(messages.DefaultControllerType) {
			t.Fatal("log was not published to the controller event inbox")
		}
	}
}

func TestExecutionLogProtocolRejectsMismatchedAndMalformedEvents(t *testing.T) {
	event, err := controllerevents.NewExecutionLog(fixtures.Log())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*messages.Message)
	}{
		{"log ID", func(m *messages.Message) { m.ID = id.New() }},
		{"agent", func(m *messages.Message) { m.AgentID = "builder-agent-ffffffff" }},
		{"command", func(m *messages.Message) { m.CorrelationID = id.New() }},
		{"work", func(m *messages.Message) { m.Work.TaskID = id.New() }},
		{"timestamp", func(m *messages.Message) { m.CreatedAt = m.CreatedAt.Add(time.Nanosecond) }},
		{"expiry", func(m *messages.Message) { m.ExpiresAt = m.CreatedAt.Add(time.Minute) }},
		{"event type", func(m *messages.Message) { m.Type = "execution.unknown" }},
		{"unknown field", func(m *messages.Message) { m.Payload = append([]byte(`{"private-field":true,`), m.Payload[1:]...) }},
		{"trailing object", func(m *messages.Message) { m.Payload = append(m.Payload, []byte(`{}`)...) }},
		{"non-object", func(m *messages.Message) { m.Payload = []byte(`[]`) }},
		{"invalid encoding", func(m *messages.Message) { m.Payload = bytes.Replace(m.Payload, []byte("Compiling"), []byte{0xff}, 1) }},
		{"oversized envelope", func(m *messages.Message) { m.Payload = append(m.Payload, bytes.Repeat([]byte(" "), 64*1024)...) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := event
			changed.Payload = bytes.Clone(event.Payload)
			test.change(&changed)
			bus := &fakes.SecretBus{}

			_, decodeErr := controllerevents.DecodeExecutionLog(changed)
			publishErr := (controllerevents.Client{Bus: bus}).Publish(t.Context(), changed)

			if decodeErr == nil || publishErr == nil || len(bus.Messages()) != 0 {
				t.Fatalf("invalid execution log reached the bus: decode=%v publish=%v", decodeErr, publishErr)
			}
			if strings.Contains(decodeErr.Error(), "private-field") || strings.Contains(publishErr.Error(), "private-field") {
				t.Fatal("invalid execution log exposed payload details")
			}
		})
	}
}
