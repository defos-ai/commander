package fakes

import (
	"context"
	"slices"
	"strings"
	"time"
)

type ProgressLogCall struct {
	Message    string
	StartedAt  time.Time
	FinishedAt time.Time
	Deadline   time.Time
	Error      error
}

type ProgressLogs struct {
	err             error
	block           bool
	blockHeartbeats bool
	calls           []ProgressLogCall
	published       chan string
}

func NewProgressLogs(err error) *ProgressLogs {
	return &ProgressLogs{err: err, published: make(chan string, 256)}
}

func (p *ProgressLogs) Block() { p.block = true }

func (p *ProgressLogs) BlockHeartbeats() { p.blockHeartbeats = true }

func (p *ProgressLogs) Log(ctx context.Context, message string) error {
	deadline, _ := ctx.Deadline()
	index := len(p.calls)
	p.calls = append(p.calls, ProgressLogCall{Message: message, StartedAt: time.Now(), Deadline: deadline})
	select {
	case p.published <- message:
	default:
	}
	err := p.err
	if p.block || p.blockHeartbeats && strings.Contains(message, " still running (elapsed ") {
		<-ctx.Done()
		err = ctx.Err()
	}
	p.calls[index].FinishedAt, p.calls[index].Error = time.Now(), err
	return err
}

func (p *ProgressLogs) Messages() []string {
	messages := make([]string, len(p.calls))
	for index, call := range p.calls {
		messages[index] = call.Message
	}
	return messages
}

func (p *ProgressLogs) Calls() []ProgressLogCall { return slices.Clone(p.calls) }

func (p *ProgressLogs) Published() <-chan string { return p.published }
