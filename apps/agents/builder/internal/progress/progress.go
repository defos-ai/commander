package progress

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
)

const heartbeatInterval = 30 * time.Second
const publicationTimeout = time.Second

func Run(ctx context.Context, stage string, work func() error) (err error) {
	started := time.Now()
	if errors.Is(publish(ctx, stage+" started"), action.ErrLogsUnavailable) {
		return work()
	}
	watching, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		heartbeats(watching, stage, started)
	}()
	finished := false
	defer func() {
		cancel()
		<-done
		message := stage + " ended with error"
		if finished && err == nil {
			message = stage + " completed"
		}
		_ = publish(ctx, message)
	}()
	err = work()
	finished = true
	return err
}

func heartbeats(ctx context.Context, stage string, started time.Time) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			_ = publish(ctx, fmt.Sprintf("%s still running (elapsed %s)", stage, time.Since(started).Round(time.Second)))
		}
	}
}

func publish(ctx context.Context, message string) error {
	publication, cancel := context.WithTimeout(ctx, publicationTimeout)
	defer cancel()
	return action.Log(publication, message)
}
