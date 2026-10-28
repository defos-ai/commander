package testing

import (
	"context"
	"errors"
	"time"

	frameworktesting "github.com/defos-ai/commander/framework/testing"
	"github.com/defos-ai/commander/providers/s3/store"
)

// Compose with framework/testing.StateCleaner so work settles before deleting files.
type TestS3Cleaner struct{ storage *store.S3Store }

var _ frameworktesting.TestCleaner = (*TestS3Cleaner)(nil)

func NewTestS3Cleaner(storage *store.S3Store) (*TestS3Cleaner, error) {
	if storage == nil {
		return nil, errors.New("test S3 cleanup requires storage")
	}
	return &TestS3Cleaner{storage: storage}, nil
}

func (c *TestS3Cleaner) Clean() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.CleanContext(ctx)
}

func (c *TestS3Cleaner) CleanContext(ctx context.Context) error {
	return c.storage.CleanTestData(ctx)
}
