package fakes

import (
	"context"
	"slices"
)

type LogCall struct {
	Context context.Context
	Message string
}

type LogProvider struct {
	calls          []LogCall
	err            error
	cancelAfterLog context.CancelFunc
}

func NewLogProvider(err error) *LogProvider { return &LogProvider{err: err} }

func (p *LogProvider) CancelAfterLog(cancel context.CancelFunc) { p.cancelAfterLog = cancel }

func (p *LogProvider) Log(ctx context.Context, message string) error {
	p.calls = append(p.calls, LogCall{Context: ctx, Message: message})
	if p.cancelAfterLog != nil {
		p.cancelAfterLog()
	}
	return p.err
}

func (p *LogProvider) Calls() []LogCall { return slices.Clone(p.calls) }
