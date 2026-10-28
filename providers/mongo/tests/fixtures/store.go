package fixtures

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/defos-ai/commander/providers/mongo/store"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func OpenStore(t *testing.T, collection string) *store.MongoStore {
	t.Helper()
	uri := os.Getenv("COMMANDER_TEST_MONGO_URL")
	if uri == "" {
		t.Skip("set COMMANDER_TEST_MONGO_URL to run against MongoDB")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	database, err := store.Open(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := database.Delete(ctx, collection, bson.D{}); err != nil {
			t.Error(err)
		}
		if err := database.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if _, err := database.Delete(ctx, collection, bson.D{}); err != nil {
		t.Fatal(err)
	}
	return database
}
