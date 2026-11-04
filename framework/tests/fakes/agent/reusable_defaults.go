package fakes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/actions"
)

type ConcurrentReusableDefaults struct {
	*action.Base
	firstCancel   chan context.CancelFunc
	startSecond   chan struct{}
	firstReturned chan error
}

func NewConcurrentReusableDefaults() *ConcurrentReusableDefaults {
	return &ConcurrentReusableDefaults{
		Base:        action.NewBase(actions.Action{Name: "join-default", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}),
		firstCancel: make(chan context.CancelFunc, 1), startSecond: make(chan struct{}, 1), firstReturned: make(chan error, 1),
	}
}

func (a *ConcurrentReusableDefaults) Run(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	first, cancel := context.WithCancel(ctx)
	defer cancel()
	a.firstCancel <- cancel
	firstDone := make(chan error, 1)
	go func() {
		value, err := action.RequestReusableSecret(first, "TOKEN", action.SecretRequestOptions{Default: []byte("first-default")})
		clear(value)
		a.firstReturned <- err
		firstDone <- err
	}()
	select {
	case <-a.startSecond:
	case <-ctx.Done():
		cancel()
		<-firstDone
		return action.Result{}, ctx.Err()
	}
	value, secondErr := action.RequestReusableSecret(ctx, "TOKEN", action.SecretRequestOptions{Default: []byte("second-default")})
	defer clear(value)
	firstErr := <-firstDone
	if secondErr != nil {
		return action.Result{}, secondErr
	}
	return action.JSONResult(struct {
		FirstCancelled bool `json:"first_cancelled"`
		SecondMatched  bool `json:"second_matched"`
	}{errors.Is(firstErr, context.Canceled), bytes.Equal(value, []byte("managed-winner"))})
}

func (a *ConcurrentReusableDefaults) StartSecond() { a.startSecond <- struct{}{} }

func (a *ConcurrentReusableDefaults) CancelFirst(ctx context.Context) error {
	select {
	case cancel := <-a.firstCancel:
		cancel()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *ConcurrentReusableDefaults) FirstReturned() <-chan error { return a.firstReturned }
