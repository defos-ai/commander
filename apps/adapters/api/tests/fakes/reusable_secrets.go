package fakes

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/actions"
)

const ReusableSecretName = "LOGIN_FOR_CRM"
const ReusableSecretDefault = "fixture-reusable-admin"
const ReusableSecretOtherDefault = "fixture-reusable-other"

type ReusableSecretAction struct {
	*action.Base
	defaultValue []byte
	expected     []byte
	calls        atomic.Int32
}

func NewReusableSecretAction(name string, defaultValue, expected []byte) *ReusableSecretAction {
	return &ReusableSecretAction{
		Base: action.NewBase(actions.Action{Name: name,
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"matched":{"type":"boolean"}},"required":["matched"],"additionalProperties":false}`),
		}),
		defaultValue: bytes.Clone(defaultValue), expected: bytes.Clone(expected),
	}
}

func (a *ReusableSecretAction) Run(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	a.calls.Add(1)
	value, err := action.RequestReusableSecret(ctx, ReusableSecretName, action.SecretRequestOptions{Default: a.defaultValue})
	defer clear(value)
	if err != nil {
		return action.Result{}, err
	}
	return action.JSONResult(struct {
		Matched bool `json:"matched"`
	}{Matched: bytes.Equal(value, a.expected)})
}

func (a *ReusableSecretAction) Calls() int32 { return a.calls.Load() }

type SecretLogs struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (l *SecretLogs) Write(value []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.Write(value)
}

func (l *SecretLogs) Snapshot() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.String()
}
