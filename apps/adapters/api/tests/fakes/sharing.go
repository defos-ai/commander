package fakes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
)

const (
	FindingAction  = "find-vulnerabilities"
	AnalysisAction = "analyze-vulnerabilities"
	EvidenceAction = "review-evidence"
	BatchAction    = "plan-investigation"
)

type SharingProducer struct {
	*action.Base
	calls  atomic.Int32
	gate   atomic.Pointer[SharingGate]
	active atomic.Pointer[SharingGate]
}

func NewSharingProducer() *SharingProducer {
	return &SharingProducer{Base: action.NewBase(actions.Action{Name: FindingAction,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"marker":{"type":"string"},"questions":{"type":"boolean"},"fail":{"type":"boolean"}},"required":["marker"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`)})}
}

func (p *SharingProducer) Run(ctx context.Context, input json.RawMessage) (action.Result, error) {
	var request struct {
		Marker    string `json:"marker"`
		Questions bool   `json:"questions"`
		Fail      bool   `json:"fail"`
	}
	if err := action.DecodeInput(input, &request); err != nil {
		return action.Result{}, err
	}
	p.calls.Add(1)
	if gate := p.gate.Swap(nil); gate != nil {
		close(gate.entered)
		select {
		case <-gate.release:
		case <-ctx.Done():
			return action.Result{}, ctx.Err()
		}
	}
	if request.Fail {
		return action.Result{}, errors.New("fixture vulnerability scan failed")
	}
	if request.Questions {
		for _, key := range []string{"scope", "priority"} {
			if _, err := action.Question(ctx, key, "Choose the "+key+" for this finding.", "Investigate", "Ignore"); err != nil {
				return action.Result{}, err
			}
		}
	}
	output, err := json.Marshal(struct {
		Marker  string `json:"marker"`
		Finding int64  `json:"finding"`
	}{Marker: request.Marker, Finding: 9007199254740993})
	if err != nil {
		return action.Result{}, err
	}
	return action.Result{
		Output: output,
		Artifacts: []action.Artifact{{Name: "finding", Files: []artifacts.FileSource{{
			Name: "finding.txt", MediaType: "text/plain", Data: []byte("evidence:" + request.Marker),
		}}}},
	}, nil
}

func (p *SharingProducer) Calls() int32 { return p.calls.Load() }

func (p *SharingProducer) BlockNextRun() *SharingGate {
	gate := &SharingGate{entered: make(chan struct{}), release: make(chan struct{})}
	p.active.Store(gate)
	p.gate.Store(gate)
	return gate
}

func (p *SharingProducer) ReleaseGate() {
	if gate := p.active.Load(); gate != nil {
		gate.Release()
	}
}

type SharingBatchPlanner struct {
	*action.Base
	gate *SharingGate
}

func NewSharingBatchPlanner() *SharingBatchPlanner {
	return &SharingBatchPlanner{Base: action.NewBase(actions.Action{Name: BatchAction,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"wait":{"type":"boolean"}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`)}),
		gate: &SharingGate{entered: make(chan struct{}), release: make(chan struct{})}}
}

func (p *SharingBatchPlanner) Run(ctx context.Context, input json.RawMessage) (action.Result, error) {
	var request struct {
		Wait bool `json:"wait"`
	}
	if err := action.DecodeInput(input, &request); err != nil {
		return action.Result{}, err
	}
	if request.Wait {
		close(p.gate.entered)
		select {
		case <-p.gate.release:
		case <-ctx.Done():
			return action.Result{}, ctx.Err()
		}
	}
	return action.JSONResult(struct{}{})
}

func (p *SharingBatchPlanner) Gate() *SharingGate { return p.gate }

type SharedFinding struct {
	Update    agentevents.SharedData
	Execution action.Execution
	Files     map[string][]byte
}

type SharingAnalyzer struct {
	*action.Base
	files    artifacts.ArtifactFileStore
	stop     context.Context
	cancel   context.CancelFunc
	received chan SharedFinding
}

func NewSharingAnalyzer(name string, files artifacts.ArtifactFileStore) *SharingAnalyzer {
	stop, cancel := context.WithCancel(context.Background())
	return &SharingAnalyzer{Base: action.NewBase(actions.Action{Name: name, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}),
		files: files, stop: stop, cancel: cancel, received: make(chan SharedFinding, 16)}
}

func (a *SharingAnalyzer) Run(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	receiving, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(a.stop, cancel)
	defer func() { stop(); cancel() }()
	execution, _ := action.ExecutionFromContext(ctx)
	count := 0
	seen := make(map[[5]string]bool)
	for {
		update, err := action.ReceiveSharedData(receiving)
		if err != nil {
			if a.stop.Err() != nil {
				return action.JSONResult(struct {
					Findings int `json:"findings"`
				}{count})
			}
			return action.Result{}, err
		}
		source := [5]string{update.Source.AgentID, update.Source.CommandID, "", update.BatchID, ""}
		if update.Decision != nil {
			source[2] = update.Decision.ID
		}
		if update.Batch != nil {
			source[4] = string(update.Batch.Type)
		}
		if seen[source] {
			continue
		}
		seen[source] = true
		finding := SharedFinding{Update: update, Execution: execution, Files: make(map[string][]byte)}
		for _, artifact := range update.Artifacts {
			for _, file := range artifact.Files {
				data, err := a.files.Get(receiving, artifact.ID, file)
				if err != nil {
					return action.Result{}, err
				}
				finding.Files[file.ID] = data
			}
		}
		select {
		case a.received <- finding:
			if update.Batch == nil {
				count++
			}
		case <-receiving.Done():
			return action.Result{}, receiving.Err()
		}
	}
}

func (a *SharingAnalyzer) Received() <-chan SharedFinding { return a.received }
func (a *SharingAnalyzer) Stop()                          { a.cancel() }

type SharingGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *SharingGate) Entered() <-chan struct{} { return g.entered }
func (g *SharingGate) Release()                 { g.once.Do(func() { close(g.release) }) }

type SharingBus struct {
	comms.Bus
	mu              sync.Mutex
	shared          []messages.Message
	failedSources   map[[2]string]bool
	uncertainAction string
	gate            atomic.Pointer[SharingGate]
	allGates        []*SharingGate
}

func NewSharingBus(bus comms.Bus) *SharingBus {
	return &SharingBus{Bus: bus, failedSources: make(map[[2]string]bool)}
}

func (b *SharingBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if message.Type == messages.RunAction {
		var call actions.ActionCall
		if err := json.Unmarshal(message.Payload, &call); err != nil {
			return err
		}
		b.mu.Lock()
		uncertain := b.uncertainAction == call.ActionName
		if uncertain {
			b.uncertainAction = ""
		}
		b.mu.Unlock()
		if err := b.Bus.Publish(ctx, topic, message); err != nil {
			return err
		}
		if uncertain {
			return errors.New("fixture action published but confirmation was lost")
		}
		return nil
	}
	if message.Type != messages.DataShared {
		return b.Bus.Publish(ctx, topic, message)
	}
	update, err := agentevents.DecodeSharedData(message)
	if err != nil {
		return err
	}
	source := [2]string{update.Source.AgentID, update.Source.CommandID}
	observed := message
	observed.Payload = bytes.Clone(message.Payload)
	b.mu.Lock()
	b.shared = append(b.shared, observed)
	failed := b.failedSources[source]
	var gate *SharingGate
	if !failed {
		gate = b.gate.Swap(nil)
		if gate != nil {
			b.failedSources[source] = true
		}
	}
	b.mu.Unlock()
	if failed {
		return errors.New("fixture shared-data transport unavailable")
	}
	if gate != nil {
		close(gate.entered)
		// A stuck broker must not hold the controller owner loop or result consumer.
		<-gate.release
		return errors.New("fixture shared-data transport unavailable")
	}
	return b.Bus.Publish(ctx, topic, message)
}

func (b *SharingBus) LoseNextActionConfirmation(actionName string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.uncertainAction = actionName
}

func (b *SharingBus) BlockNextSharedData() *SharingGate {
	gate := &SharingGate{entered: make(chan struct{}), release: make(chan struct{})}
	b.mu.Lock()
	b.allGates = append(b.allGates, gate)
	b.mu.Unlock()
	b.gate.Store(gate)
	return gate
}

func (b *SharingBus) ReleaseGates() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, gate := range b.allGates {
		gate.Release()
	}
}

func (b *SharingBus) SharedMessages() []messages.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	copy := make([]messages.Message, len(b.shared))
	for i, message := range b.shared {
		message.Payload = bytes.Clone(message.Payload)
		copy[i] = message
	}
	return copy
}

type SharingArtifactStore struct {
	artifacts.ArtifactStore
	gate   atomic.Pointer[SharingGate]
	active atomic.Pointer[SharingGate]
}

func (s *SharingArtifactStore) Get(ctx context.Context, id string) (artifacts.Artifact, error) {
	if gate := s.gate.Swap(nil); gate != nil {
		close(gate.entered)
		select {
		case <-gate.release:
		case <-ctx.Done():
			return artifacts.Artifact{}, ctx.Err()
		}
	}
	return s.ArtifactStore.Get(ctx, id)
}

func (s *SharingArtifactStore) BlockNextRead() *SharingGate {
	gate := &SharingGate{entered: make(chan struct{}), release: make(chan struct{})}
	s.active.Store(gate)
	s.gate.Store(gate)
	return gate
}

func (s *SharingArtifactStore) ReleaseGate() {
	if gate := s.active.Load(); gate != nil {
		gate.Release()
	}
}
