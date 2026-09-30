package artifacts

import (
	"context"
	"errors"
	"fmt"

	"commander/framework/artifact"
	artifactstore "commander/framework/artifact/store"
	"commander/framework/id"
	"commander/providers/mongo/internal/bsonvalue"
	"commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const collection = "artifacts"

type ArtifactMongoStore struct{ database *store.MongoStore }

var _ artifactstore.ArtifactStore = (*ArtifactMongoStore)(nil)

type artifactDocument struct {
	ID         string                  `bson:"_id"`
	Name       string                  `bson:"name"`
	AgentID    string                  `bson:"agent_id"`
	ActionName string                  `bson:"action_name"`
	CommandID  string                  `bson:"command_id,omitempty"`
	CreatedAt  bsonvalue.Timestamp     `bson:"created_at"`
	Files      []artifact.ArtifactFile `bson:"files"`
}

func NewArtifactMongoStore(ctx context.Context, database *store.MongoStore) (*ArtifactMongoStore, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if database == nil {
		return nil, errors.New("artifact database is required")
	}
	if err := database.EnsureIndexes(ctx, collection, []mongo.IndexModel{{
		Keys: bson.D{{Key: "agent_id", Value: 1}, {Key: "command_id", Value: 1}},
	}}); err != nil {
		return nil, err
	}
	return &ArtifactMongoStore{database: database}, nil
}

func (s *ArtifactMongoStore) Create(ctx context.Context, value artifact.Artifact) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	document := artifactDocument{ID: value.ID, Name: value.Name, AgentID: value.AgentID,
		ActionName: value.ActionName, CommandID: value.CommandID,
		CreatedAt: bsonvalue.NewTimestamp(value.CreatedAt), Files: value.Files}
	err := s.database.Insert(ctx, collection, document)
	if !mongo.IsDuplicateKeyError(err) {
		return err
	}
	stored, err := s.Get(ctx, value.ID)
	if err != nil {
		return err
	}
	if !stored.Equal(value) {
		return artifact.ErrArtifactConflict
	}
	return nil
}

func (s *ArtifactMongoStore) Get(ctx context.Context, artifactID string) (artifact.Artifact, error) {
	if err := checkContext(ctx); err != nil {
		return artifact.Artifact{}, err
	}
	if !id.Valid(artifactID) {
		return artifact.Artifact{}, errors.New("artifact ID must be a canonical UUID")
	}
	var document artifactDocument
	if err := s.database.GetByID(ctx, collection, artifactID, &document); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return artifact.Artifact{}, artifact.ErrUnknownArtifact
		}
		return artifact.Artifact{}, err
	}
	createdAt, err := document.CreatedAt.Decode()
	if err != nil {
		return artifact.Artifact{}, fmt.Errorf("stored artifact timestamp: %w", err)
	}
	value := artifact.Artifact{ID: document.ID, Name: document.Name, AgentID: document.AgentID,
		ActionName: document.ActionName, CommandID: document.CommandID,
		CreatedAt: createdAt, Files: document.Files}
	if err := value.Validate(); err != nil {
		return artifact.Artifact{}, fmt.Errorf("invalid stored artifact: %w", err)
	}
	return value, nil
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("artifact database requires a context")
	}
	return ctx.Err()
}
