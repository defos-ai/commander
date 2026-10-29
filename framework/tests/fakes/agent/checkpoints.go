package fakes

import (
	"context"
	"encoding/json"
	"testing"
)

type UnexpectedCheckpointProvider struct{ t testing.TB }

func (p *UnexpectedCheckpointProvider) LoadCheckpoint(context.Context, string) (json.RawMessage, bool, error) {
	p.t.Error("standalone work called an inherited checkpoint provider")
	return nil, false, nil
}
func (p *UnexpectedCheckpointProvider) SaveCheckpoint(context.Context, string, json.RawMessage) error {
	p.t.Error("standalone work called an inherited checkpoint provider")
	return nil
}

func NewUnexpectedCheckpointProvider(t testing.TB) *UnexpectedCheckpointProvider {
	return &UnexpectedCheckpointProvider{t: t}
}
