package fakes

import (
	"context"
	"slices"
)

type ProgressLogs struct {
	err       error
	messages  []string
	published chan string
}

func NewProgressLogs(err error) *ProgressLogs {
	return &ProgressLogs{err: err, published: make(chan string, 256)}
}

func (p *ProgressLogs) Log(_ context.Context, message string) error {
	p.messages = append(p.messages, message)
	select {
	case p.published <- message:
	default:
	}
	return p.err
}

func (p *ProgressLogs) Messages() []string { return slices.Clone(p.messages) }

func (p *ProgressLogs) Published() <-chan string { return p.published }
