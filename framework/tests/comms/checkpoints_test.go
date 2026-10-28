package comms_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/comms"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/comms"
)

func TestCheckpointProtocolPreservesExecutionAndRequestIdentity(t *testing.T) {
	request := fixtures.CheckpointWireRequest(t)
	payload, err := controllerevents.DecodeCheckpointRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := agentevents.NewCheckpointResponse(request, agentevents.CheckpointResponse{
		CommandID: payload.CommandID, Name: payload.Name, Data: payload.Data,
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := agentevents.DecodeCheckpointResponse(response)
	if err != nil || decoded.CommandID != request.CorrelationID || response.CorrelationID != request.ID ||
		response.Work != request.Work || response.AgentID != request.AgentID {
		t.Fatal("checkpoint reply lost its original execution or request identity")
	}
	bus := &fakes.SecretBus{}
	if err := (controllerevents.Client{Bus: bus}).Publish(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := (agentevents.Client{Bus: bus}).Publish(t.Context(), response); err != nil {
		t.Fatal(err)
	}
	if bus.Topics()[0] != messages.ControllerEvents(messages.DefaultControllerType) || bus.Topics()[1] != messages.AgentEvents(request.AgentID) {
		t.Fatal("checkpoint exchange used the action command channel")
	}
	before := bytes.Clone(response.Payload)
	decoded.Data[0] = '['
	if !bytes.Equal(before, response.Payload) {
		t.Fatal("decoded checkpoint data mutated prepared wire data")
	}
}

func TestCheckpointRequestRejectsMalformedAndUnboundedData(t *testing.T) {
	request := fixtures.CheckpointWireRequest(t)
	for _, test := range []struct {
		name   string
		change func(*messages.Message, *controllerevents.CheckpointRequest)
	}{
		{"command correlation", func(m *messages.Message, _ *controllerevents.CheckpointRequest) { m.CorrelationID = id.New() }},
		{"expiry", func(m *messages.Message, _ *controllerevents.CheckpointRequest) {
			m.ExpiresAt = time.Now().Add(time.Minute)
		}},
		{"load with data", func(_ *messages.Message, p *controllerevents.CheckpointRequest) {
			p.Operation = controllerevents.CheckpointLoad
		}},
		{"unknown operation", func(_ *messages.Message, p *controllerevents.CheckpointRequest) { p.Operation = "replace" }},
		{"invalid name", func(_ *messages.Message, p *controllerevents.CheckpointRequest) { p.Name = "../unsafe" }},
		{"non-object", func(_ *messages.Message, p *controllerevents.CheckpointRequest) {
			p.Data = json.RawMessage(`"private-data"`)
		}},
		{"too large", func(_ *messages.Message, p *controllerevents.CheckpointRequest) {
			p.Data = json.RawMessage(`{"value":"` + strings.Repeat("x", executions.MaxCheckpointSize) + `"}`)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := request
			payload, _ := controllerevents.DecodeCheckpointRequest(request)
			test.change(&changed, &payload)
			changed.Payload = fixtures.SecretWirePayload(t, payload)
			if _, err := controllerevents.DecodeCheckpointRequest(changed); err == nil || strings.Contains(err.Error(), "private-data") {
				t.Fatal("invalid checkpoint request was accepted or exposed data")
			}
		})
	}
	request.Payload = append(bytes.Clone(request.Payload[:len(request.Payload)-1]), []byte(`,"private-data":true}`)...)
	if _, err := controllerevents.DecodeCheckpointRequest(request); err == nil || strings.Contains(err.Error(), "private-data") {
		t.Fatal("unknown wire field was accepted or echoed")
	}
}

func TestCheckpointResponsesRequireBoundedDataOrFixedError(t *testing.T) {
	request := fixtures.CheckpointWireRequest(t)
	payload, _ := controllerevents.DecodeCheckpointRequest(request)
	for _, test := range []struct {
		name   string
		change func(*agentevents.CheckpointResponse)
	}{
		{"missing success data", func(p *agentevents.CheckpointResponse) { p.Data = nil }},
		{"error with data", func(p *agentevents.CheckpointResponse) { p.Error = agentevents.CheckpointConflict }},
		{"arbitrary error", func(p *agentevents.CheckpointResponse) { p.Data, p.Error = nil, "private-diagnostic" }},
		{"wrong command", func(p *agentevents.CheckpointResponse) { p.CommandID = id.New() }},
		{"wrong name", func(p *agentevents.CheckpointResponse) { p.Name = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := agentevents.CheckpointResponse{CommandID: payload.CommandID, Name: payload.Name, Data: payload.Data}
			test.change(&response)
			if _, err := agentevents.NewCheckpointResponse(request, response); err == nil || strings.Contains(err.Error(), "private-diagnostic") {
				t.Fatal("invalid response was accepted or arbitrary diagnostic exposed")
			}
		})
	}
	for _, code := range []string{
		agentevents.CheckpointNotFound, agentevents.CheckpointUnknownExecution, agentevents.CheckpointConflict,
		agentevents.CheckpointCompleted, agentevents.CheckpointInvalid,
	} {
		response, err := agentevents.NewCheckpointResponse(request, agentevents.CheckpointResponse{
			CommandID: payload.CommandID, Name: payload.Name, Error: code,
		})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := agentevents.DecodeCheckpointResponse(response)
		if err != nil || decoded.Error != code || len(decoded.Data) != 0 {
			t.Fatal("fixed checkpoint error was not preserved")
		}
	}
}
