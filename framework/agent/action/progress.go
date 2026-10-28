package action

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const progressHeartbeatInterval = 30 * time.Second
const progressPublicationTimeout = time.Second

// RunWithProgress executes work once with best-effort logs for a fixed, non-sensitive
// stage label. It reports liveness every 30 seconds, bounds publications to one
// second, and joins its reporter before returning work's unchanged error.
func RunWithProgress(ctx context.Context, stage string, work func() error) (err error) {
	started := time.Now()
	if errors.Is(publishProgress(ctx, stage+" started"), ErrLogsUnavailable) {
		return work()
	}
	watching, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		progressHeartbeats(watching, stage, started)
	}()
	finished := false
	defer func() {
		cancel()
		<-done
		message := stage + " ended with error"
		if finished && err == nil {
			message = stage + " completed"
		}
		_ = publishProgress(ctx, message)
	}()
	err = work()
	finished = true
	return err
}

func progressHeartbeats(ctx context.Context, stage string, started time.Time) {
	ticker := time.NewTicker(progressHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			_ = publishProgress(ctx, fmt.Sprintf("%s still running (elapsed %s)", stage, time.Since(started).Round(time.Second)))
		}
	}
}

func publishProgress(ctx context.Context, message string) error {
	publication, cancel := context.WithTimeout(ctx, progressPublicationTimeout)
	defer cancel()
	return Log(publication, message)
}
