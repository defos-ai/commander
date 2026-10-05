package testing

import (
	"context"
	"errors"
	"fmt"
	"time"

	frameworktesting "commander/framework/testing"
	"commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestMongoCleaner removes public operation and artifact records from the
// configured test database. Collections, indexes, catalogs, and executions stay.
// Compose it with framework/testing.StateCleaner before exposing test cleanup.
type TestMongoCleaner struct{ database *store.MongoStore }

var _ frameworktesting.TestCleaner = (*TestMongoCleaner)(nil)

func NewTestMongoCleaner(database *store.MongoStore) (*TestMongoCleaner, error) {
	if database == nil {
		return nil, errors.New("test MongoDB cleanup requires a database")
	}
	return &TestMongoCleaner{database: database}, nil
}

func (c *TestMongoCleaner) Clean() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.CleanContext(ctx)
}

func (c *TestMongoCleaner) CleanContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("test MongoDB cleanup requires a context")
	}
	return c.database.WithTransaction(ctx, func(ctx context.Context) error {
		for _, collection := range []string{"operation_tasks", "operation_questions", "operation_events", "operations"} {
			if _, err := c.database.Delete(ctx, collection, bson.D{}); err != nil {
				return fmt.Errorf("clear test %s: %w", collection, err)
			}
		}
		var artifacts []struct {
			ID string `bson:"_id"`
		}
		if err := c.database.Find(ctx, "artifacts", bson.M{"test_deleted": bson.M{"$ne": true}}, &artifacts); err != nil {
			return fmt.Errorf("read test artifacts: %w", err)
		}
		for _, artifact := range artifacts {
			// The private marker preserves identity like execution deduplication,
			// while public reads treat the metadata as absent.
			if _, err := c.database.Replace(ctx, "artifacts", bson.M{"_id": artifact.ID},
				bson.M{"_id": artifact.ID, "test_deleted": true}, false); err != nil {
				return fmt.Errorf("retire test artifact: %w", err)
			}
		}
		return nil
	})
}
