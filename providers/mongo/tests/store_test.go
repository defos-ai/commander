package mongo_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func openStore(t *testing.T, collection string) *store.MongoStore {
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

func TestMongoQueriesAndUpdates(t *testing.T) {
	const collection = "store_tests"
	database := openStore(t, collection)
	ctx := t.Context()
	type item struct {
		ID    string `bson:"_id"`
		Name  string `bson:"name"`
		Value int    `bson:"value"`
	}
	if err := database.EnsureIndexes(ctx, collection, []mongo.IndexModel{{Keys: bson.D{{Key: "name", Value: 1}}, Options: options.Index().SetUnique(true)}}); err != nil {
		t.Fatal(err)
	}
	for _, doc := range []item{{"a", "first", 1}, {"b", "second", 2}, {"c", "third", 3}} {
		if err := database.Insert(ctx, collection, doc); err != nil {
			t.Fatal(err)
		}
	}

	var page []item
	err := database.Find(ctx, collection, bson.M{"value": bson.M{"$gte": 1}}, &page,
		options.Find().SetSort(bson.D{{Key: "value", Value: -1}}).SetSkip(1).SetLimit(1).SetProjection(bson.M{"name": 0}))
	if err != nil || len(page) != 1 || page[0].ID != "b" || page[0].Value != 2 || page[0].Name != "" {
		t.Fatalf("filtered and projected page: %+v, error=%v", page, err)
	}
	var found item
	if err := database.FindOne(ctx, collection, bson.M{"name": "third"}, &found); err != nil || found.ID != "c" {
		t.Fatalf("find one: %+v, error=%v", found, err)
	}
	updated, err := database.Update(ctx, collection, bson.M{"_id": "a", "value": 1}, bson.M{"$inc": bson.M{"value": 4}})
	if err != nil || updated.MatchedCount != 1 {
		t.Fatalf("conditional update: %+v, error=%v", updated, err)
	}
	stale, err := database.Update(ctx, collection, bson.M{"_id": "a", "value": 1}, bson.M{"$inc": bson.M{"value": 100}})
	if err != nil || stale.MatchedCount != 0 {
		t.Fatalf("stale update changed a document: %+v, error=%v", stale, err)
	}
	for range 2 {
		if _, err := database.Upsert(ctx, collection, bson.M{"_id": "d"}, bson.M{"$set": bson.M{"name": "fourth", "value": 4}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Insert(ctx, collection, item{"duplicate", "fourth", 99}); !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("unique index did not reject duplicate: %v", err)
	}
	if err := database.GetByID(ctx, collection, "a", &found); err != nil || found.Value != 5 {
		t.Fatalf("conditional update was lost: %+v, error=%v", found, err)
	}
	deleted, err := database.Delete(ctx, collection, bson.M{"value": bson.M{"$gte": 4}})
	if err != nil || deleted.DeletedCount != 2 {
		t.Fatalf("delete filter: %+v, error=%v", deleted, err)
	}
	if err := database.GetByID(ctx, collection, "d", &found); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("deleted document is still readable: %v", err)
	}
}

func TestMongoLookupTreatsIDsAsLiteralValues(t *testing.T) {
	const collection = "literal_id_tests"
	database := openStore(t, collection)
	ctx := t.Context()
	ids := []string{"ordinary", `{"$ne":null}`, `{"$gt":""}`, "$where", "nested.id"}
	for _, id := range ids {
		if err := database.Insert(ctx, collection, bson.M{"_id": id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		var found struct {
			ID string `bson:"_id"`
		}
		if err := database.GetByID(ctx, collection, id, &found); err != nil || found.ID != id {
			t.Fatalf("literal ID %q: found=%q, error=%v", id, found.ID, err)
		}
	}
	var found bson.M
	if err := database.GetByID(ctx, collection, `{"$exists":true}`, &found); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("operator-looking ID matched a different document: %v", err)
	}
}

func TestMongoTransactionCommitAndRollback(t *testing.T) {
	const collection = "transaction_tests"
	database := openStore(t, collection)
	ctx := t.Context()
	// Create the collection before entering the first transaction.
	if err := database.Insert(ctx, collection, bson.M{"_id": "existing", "value": 0}); err != nil {
		t.Fatal(err)
	}
	abort := errors.New("abort fixture transaction")

	for _, commit := range []bool{false, true} {
		err := database.WithTransaction(ctx, func(tx context.Context) error {
			if err := database.Insert(tx, collection, bson.M{"_id": "created", "value": 1}); err != nil {
				return err
			}
			if _, err := database.Update(tx, collection, bson.M{"_id": "existing"}, bson.M{"$set": bson.M{"value": 1}}); err != nil {
				return err
			}
			if !commit {
				return abort
			}
			return nil
		})

		if commit && err != nil || !commit && !errors.Is(err, abort) {
			t.Fatalf("commit=%v: %v", commit, err)
		}
		var existing struct{ Value int }
		if err := database.GetByID(ctx, collection, "existing", &existing); err != nil {
			t.Fatal(err)
		}
		var created bson.M
		err = database.GetByID(ctx, collection, "created", &created)
		if commit && (err != nil || existing.Value != 1) || !commit && (!errors.Is(err, mongo.ErrNoDocuments) || existing.Value != 0) {
			t.Fatalf("partial transaction: commit=%v existing=%+v created=%+v error=%v", commit, existing, created, err)
		}
	}
}

func TestMongoConnectionRejectsInvalidConfiguration(t *testing.T) {
	for _, uri := range []string{"", "file:///state/database", "mongodb://localhost", "mongodb://localhost/", "mongodb://user:secret@localhost/invalid/database"} {
		t.Run(uri, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			database, err := store.Open(ctx, uri)
			if database != nil {
				_ = database.Close(ctx)
			}
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid configuration should return a credential-free error: %v", err)
			}
		})
	}
}

func TestMongoConnectionRedactsDriverErrors(t *testing.T) {
	const secret = "provider-private-password"
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	uri := "mongodb://user:" + secret + "@127.0.0.1:1/test?tlsCertificateKeyFilePassword=" + secret + "%zz"
	database, err := store.Open(ctx, uri)
	if database != nil {
		_ = database.Close(ctx)
	}
	if err == nil {
		t.Fatal("malformed URI option must fail initialization")
	}
	if strings.Contains(err.Error(), secret) {
		t.Error("initialization error exposed a sensitive URI option")
	}
	if errors.Unwrap(err) == nil {
		t.Error("initialization error must preserve its cause for inspection")
	}
}

func TestMongoConnectionAcceptsSeedLists(t *testing.T) {
	for _, hosts := range []string{"127.0.0.1:1,127.0.0.1", "[::1]:1,[::1]"} {
		t.Run(hosts, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			uri := "mongodb://" + hosts + "/test?replicaSet=unavailable-fixture&serverSelectionTimeoutMS=50"
			database, err := store.Open(ctx, uri)
			if database != nil {
				_ = database.Close(ctx)
			}
			if !mongo.IsTimeout(err) {
				t.Fatalf("valid seed list should reach server selection, got %v", err)
			}
		})
	}
}
