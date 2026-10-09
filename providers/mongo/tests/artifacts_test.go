package mongo_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
	mongoartifacts "github.com/defos-ai/commander/providers/mongo/artifacts"
	mongotesting "github.com/defos-ai/commander/providers/mongo/testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestArtifactMongoStore(t *testing.T) {
	database := openStore(t, "artifacts")
	catalog, err := mongoartifacts.NewArtifactMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	value := mongoArtifactFixture()
	if _, err := catalog.Get(t.Context(), value.ID); !errors.Is(err, artifacts.ErrUnknownArtifact) {
		t.Fatalf("missing artifact: %v", err)
	}

	if err := catalog.Create(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	replay := value.Clone()
	replay.CreatedAt = value.CreatedAt.In(time.FixedZone("other", 3600))
	if err := catalog.Create(t.Context(), replay); err != nil {
		t.Fatalf("replay with equivalent timestamp: %v", err)
	}
	for _, field := range []string{"owner", "file", "timestamp"} {
		changed := value.Clone()
		switch field {
		case "owner":
			changed.CommandID = id.MessageID()
		case "file":
			changed.Files[0].Name = "other.png"
		case "timestamp":
			changed.CreatedAt = changed.CreatedAt.Add(time.Nanosecond)
		}
		if err := catalog.Create(t.Context(), changed); !errors.Is(err, artifacts.ErrArtifactConflict) {
			t.Fatalf("changed %s replaced artifact: %v", field, err)
		}
	}

	stored, err := catalog.Get(t.Context(), value.ID)
	if err != nil || !stored.Equal(value) {
		t.Fatalf("metadata changed on roundtrip: %+v, %v", stored, err)
	}
	stored.Files[0].Name = "reader mutation"
	value.Files[0].Name = "writer mutation"
	unchanged, err := catalog.Get(t.Context(), value.ID)
	if err != nil || !unchanged.Equal(replay) {
		t.Fatalf("caller mutated stored files: %+v, %v", unchanged, err)
	}
	var document bson.M
	if err := database.GetByID(t.Context(), "artifacts", value.ID, &document); err != nil {
		t.Fatal(err)
	}
	if document["agent_id"] != value.AgentID || document["command_id"] != value.CommandID {
		t.Fatalf("owner fields are not independently queryable: %+v", document)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := catalog.Create(cancelled, mongoArtifactFixture()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled create: %v", err)
	}
	if _, err := catalog.Get(cancelled, value.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	if _, err := catalog.Get(t.Context(), "invalid"); err == nil {
		t.Fatal("invalid artifact ID accepted")
	}
	if _, err := database.Update(t.Context(), "artifacts", bson.M{"_id": value.ID}, bson.M{"$set": bson.M{"created_at.nanoseconds": 1_000_000}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Get(t.Context(), value.ID); err == nil {
		t.Fatal("corrupted timestamp accepted")
	}
}

func TestConcurrentArtifactMongoCreates(t *testing.T) {
	database := openStore(t, "artifacts")
	first, err := mongoartifacts.NewArtifactMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mongoartifacts.NewArtifactMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	value := mongoArtifactFixture()
	changed := value.Clone()
	changed.Name = "conflicting evidence"
	gate, results := make(chan struct{}), make(chan error, 12)

	for i := range 12 {
		store, candidate := first, value
		if i%2 != 0 {
			store, candidate = second, changed
		}
		go func() {
			<-gate
			results <- store.Create(t.Context(), candidate)
		}()
	}
	close(gate)
	successes, conflicts := 0, 0
	for range 12 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, artifacts.ErrArtifactConflict):
			conflicts++
		default:
			t.Fatalf("concurrent create: %v", err)
		}
	}

	if successes != 6 || conflicts != 6 {
		t.Fatalf("conflicting creates: successes=%d, conflicts=%d", successes, conflicts)
	}
	stored, err := second.Get(t.Context(), value.ID)
	if err != nil || !stored.Equal(value) && !stored.Equal(changed) {
		t.Fatalf("concurrent writes corrupted metadata: %+v, %v", stored, err)
	}
	var documents []bson.M
	if err := database.Find(t.Context(), "artifacts", bson.D{}, &documents); err != nil || len(documents) != 1 {
		t.Fatalf("replay created duplicate records: count=%d, %v", len(documents), err)
	}
}

func TestArtifactMongoCleanupKeepsDelayedResultsRetired(t *testing.T) {
	database := openStore(t, "artifacts")
	catalog, err := mongoartifacts.NewArtifactMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	value := mongoArtifactFixture()
	if err := catalog.Create(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	cleaner, err := mongotesting.NewTestMongoCleaner(database)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := cleaner.CleanContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		reopened, err := mongoartifacts.NewArtifactMongoStore(t.Context(), database)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reopened.Get(t.Context(), value.ID); !errors.Is(err, artifacts.ErrUnknownArtifact) {
			t.Fatalf("cleanup left public artifact metadata: %v", err)
		}
		if err := reopened.Create(t.Context(), value); err != nil {
			t.Fatalf("delayed result must be acknowledged: %v", err)
		}
		if _, err := reopened.Get(t.Context(), value.ID); !errors.Is(err, artifacts.ErrUnknownArtifact) {
			t.Fatalf("delayed result restored retired metadata: %v", err)
		}
	}
	fresh := mongoArtifactFixture()
	if err := catalog.Create(t.Context(), fresh); err != nil {
		t.Fatalf("cleanup prevented new artifacts: %v", err)
	}
	if saved, err := catalog.Get(t.Context(), fresh.ID); err != nil || !saved.Equal(fresh) {
		t.Fatalf("new artifact did not survive retirement of old results: %+v, %v", saved, err)
	}
}

func mongoArtifactFixture() artifacts.Artifact {
	return artifacts.Artifact{ID: id.ArtifactID(), Name: "screenshots", AgentID: "browser-agent-1234abcd",
		ActionName: "capture-page", CommandID: id.MessageID(), CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC),
		Files: []artifacts.ArtifactFile{
			{ID: id.ArtifactFileID(), Name: "desktop.png", MediaType: "image/png", Size: 128, SHA256: strings.Repeat("a", 64)},
			{ID: id.ArtifactFileID(), Name: "mobile.png", MediaType: "image/png", Size: 64, SHA256: strings.Repeat("b", 64)},
		}}
}
