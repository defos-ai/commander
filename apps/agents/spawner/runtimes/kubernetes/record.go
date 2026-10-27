package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"time"
	"unicode/utf8"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/agent/action"
)

// The intent is immutable and contains the complete desired body, never provider
// credentials or server-assigned identity. Receipts are separate immutable facts.
type record struct {
	Runtime   runtimes.RuntimeKind `json:"runtime"`
	RequestID string               `json:"request_id"`
	Package   string               `json:"package"`
	AgentID   string               `json:"agent_id"`
	AgentType string               `json:"agent_type"`
	Image     string               `json:"image"`
	Owner     string               `json:"owner"`
	Pod       Pod                  `json:"pod"`
}

func (r record) spec() Spec {
	return Spec{RequestID: r.RequestID, Owner: r.Owner, AgentID: r.AgentID,
		AgentType: r.AgentType, Package: r.Package, Image: r.Image}
}

func (m *Manager) readRecord(ctx context.Context, requestID, reference string) (record, bool, error) {
	data, found, err := action.LoadCheckpoint(ctx, "launch-intent")
	if err != nil || !found {
		return record{}, found, err
	}
	invalid := errors.New("Kubernetes launch record is invalid or incomplete; refusing another Pod")
	var saved record
	execution, active := action.ExecutionFromContext(ctx)
	if decodeCheckpoint(data, &saved) != nil || saved.Runtime != runtimes.RuntimeKubernetes ||
		saved.RequestID != requestID || !active || requestID != "command:"+execution.CommandID ||
		execution.Work.Validate() != nil || validatePodLaunch(saved.spec()) != nil {
		return record{}, true, invalid
	}
	if saved.Package != reference {
		return record{}, true, errors.New("launch request already belongs to a different package")
	}
	desired, err := BuildPod(m.config.Pod, saved.spec())
	if err != nil || !sameJSON(saved.Pod, desired) {
		return record{}, true, errors.New("recorded Kubernetes Pod differs from spawner configuration")
	}
	return saved, true, nil
}

// The caller holds Manager.mu. Only a confirmed save grants this host activation
// authority; observing a possibly persisted write after an error does not.
func (m *Manager) reserve(ctx context.Context, saved record) error {
	data, err := json.Marshal(saved)
	if err != nil {
		return errors.New("encode Kubernetes launch record")
	}
	if err := action.SaveCheckpoint(ctx, "launch-intent", data); err != nil {
		return err
	}
	m.ownedIntents[saved.RequestID] = saved.Owner
	return nil
}

type receipt struct {
	Runtime   runtimes.RuntimeKind `json:"runtime"`
	RequestID string               `json:"request_id"`
	AgentID   string               `json:"agent_id"`
	Namespace string               `json:"namespace"`
	PodName   string               `json:"pod_name"`
	PodUID    string               `json:"pod_uid"`
	Owner     string               `json:"owner"`
}

func (m *Manager) readReceipt(ctx context.Context, saved record) (receipt, bool, error) {
	data, found, err := action.LoadCheckpoint(ctx, "launch-receipt")
	if err != nil || !found {
		return receipt{}, found, err
	}
	var confirmed receipt
	if decodeCheckpoint(data, &confirmed) != nil || confirmed.Runtime != runtimes.RuntimeKubernetes ||
		confirmed.RequestID != saved.RequestID || confirmed.AgentID != saved.AgentID ||
		confirmed.Namespace != saved.Pod.Metadata.Namespace || confirmed.PodName != saved.Pod.Metadata.Name ||
		confirmed.Owner != saved.Owner || !opaqueVersion(confirmed.PodUID) {
		return receipt{}, true, errors.New("Kubernetes Pod receipt is invalid or belongs to another launch")
	}
	return confirmed, true, nil
}

// The Manager must verify the admitted Pod and activation authority first. In
// particular, a missing receipt never authorizes adopting an already ungated Pod.
func (m *Manager) confirmReceipt(ctx context.Context, saved record, actual Pod) error {
	if actual.Metadata.Namespace != saved.Pod.Metadata.Namespace || actual.Metadata.Name != saved.Pod.Metadata.Name ||
		!opaqueVersion(actual.Metadata.UID) {
		return errors.New("Kubernetes Pod identity does not match the launch intent")
	}
	expected := receipt{Runtime: runtimes.RuntimeKubernetes, RequestID: saved.RequestID,
		AgentID: saved.AgentID, Namespace: saved.Pod.Metadata.Namespace,
		PodName: saved.Pod.Metadata.Name, PodUID: actual.Metadata.UID, Owner: saved.Owner}
	confirmed, found, err := m.readReceipt(ctx, saved)
	if err != nil {
		return err
	}
	if found && confirmed != expected {
		return errors.New("Kubernetes Pod UID differs from the immutable launch receipt")
	}
	data, err := json.Marshal(expected)
	if err != nil {
		return errors.New("encode Kubernetes Pod receipt")
	}
	// An identical save confirms persistence after an uncertain earlier response.
	return action.SaveCheckpoint(ctx, "launch-receipt", data)
}

type instance struct {
	Runtime     runtimes.RuntimeKind `json:"runtime"`
	RequestID   string               `json:"request_id"`
	AgentID     string               `json:"agent_id"`
	PodUID      string               `json:"pod_uid"`
	ContainerID string               `json:"container_id"`
	StartedAt   string               `json:"started_at"`
}

func (m *Manager) confirmInstance(ctx context.Context, saved record, actual Pod, state PodState) error {
	expected, err := m.rememberInstance(saved, actual, state)
	if err != nil {
		return err
	}
	data, found, err := action.LoadCheckpoint(ctx, "launch-instance")
	if err != nil {
		return err
	}
	if found {
		var confirmed instance
		if decodeCheckpoint(data, &confirmed) != nil || confirmed.Runtime != runtimes.RuntimeKubernetes ||
			!opaqueVersion(confirmed.PodUID) || !opaqueVersion(confirmed.ContainerID) ||
			!validStartedAt(confirmed.StartedAt) || confirmed != expected {
			return errors.New("Kubernetes running container differs from the immutable launch instance")
		}
	}
	data, err = json.Marshal(expected)
	if err != nil {
		return errors.New("encode Kubernetes running container identity")
	}
	return action.SaveCheckpoint(ctx, "launch-instance", data)
}

func (m *Manager) rememberInstance(saved record, actual Pod, state PodState) (instance, error) {
	if actual.Metadata.Namespace != saved.Pod.Metadata.Namespace || actual.Metadata.Name != saved.Pod.Metadata.Name ||
		!opaqueVersion(actual.Metadata.UID) || !opaqueVersion(state.ContainerID) || !validStartedAt(state.StartedAt) {
		return instance{}, errors.New("Kubernetes running container identity is invalid")
	}
	expected := instance{Runtime: runtimes.RuntimeKubernetes, RequestID: saved.RequestID,
		AgentID: saved.AgentID, PodUID: actual.Metadata.UID,
		ContainerID: state.ContainerID, StartedAt: state.StartedAt}
	if known, found := m.instances[saved.RequestID]; found && known != expected {
		return instance{}, errors.New("observed Kubernetes container changed; a replacement requires a new task")
	}
	// Preserve positive observations even if the subsequent checkpoint save fails.
	m.instances[saved.RequestID] = expected
	return expected, nil
}

func validStartedAt(value string) bool {
	started, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && !started.IsZero()
}

func decodeCheckpoint(data []byte, target any) error {
	if len(data) > 64*1024 || !utf8.Valid(data) || !jsonObject(data) {
		return errors.New("invalid Kubernetes checkpoint")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid Kubernetes checkpoint")
	}
	return nil
}

func sameJSON(left, right any) bool {
	decode := func(value any) (any, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		err = decoder.Decode(&decoded)
		return decoded, err
	}
	a, leftErr := decode(left)
	b, rightErr := decode(right)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(a, b)
}
