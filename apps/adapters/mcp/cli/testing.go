package cli

import (
	"context"
	"sync"
	"time"

	"commander/framework/domain/missions"
	frameworktesting "commander/framework/testing"
)

// Keep configured mission definitions available after clearing test data.
// Bus connections and host memory are deliberately outside this cleanup.
type testCleaner struct {
	mu       sync.Mutex
	missions missions.MissionStore
	state    frameworktesting.TestCleaner
}

func (c *testCleaner) Clean() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	definitions, err := c.missions.List(ctx)
	if err != nil {
		return err
	}
	if err := c.state.Clean(); err != nil {
		return err
	}
	for _, definition := range definitions {
		if err := c.missions.Upsert(ctx, definition); err != nil {
			return err
		}
	}
	return nil
}
