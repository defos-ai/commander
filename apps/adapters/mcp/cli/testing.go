package cli

import (
	"context"
	"time"

	"commander/framework/domain/missions"
	frameworktesting "commander/framework/testing"
)

// Keep configured mission definitions available after clearing test data.
// Call between settled tests; cleanup requests must not run concurrently.
type testCleaner struct {
	missions missions.MissionStore
	state    frameworktesting.TestCleaner
}

func (c *testCleaner) Clean() error {
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
