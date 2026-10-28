package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"

	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/providers/mongo/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const agentSourcesCollection = "agent_sources"

type AgentSourceMongoStore struct{ database *store.MongoStore }

var _ agentmodel.AgentSourceStore = (*AgentSourceMongoStore)(nil)

// JSON retains nanosecond timestamps and all uint64 versions. The indexed
// version is decimal text so BSON's signed integer range cannot truncate it.
type agentSourceDocument struct {
	ID      string          `bson:"_id"`
	Version string          `bson:"version"`
	Data    json.RawMessage `bson:"data"`
	Extra   bson.M          `bson:",inline"`
}

func NewAgentSourceMongoStore(database *store.MongoStore) *AgentSourceMongoStore {
	return &AgentSourceMongoStore{database: database}
}

func (s *AgentSourceMongoStore) Create(ctx context.Context, entry agentmodel.AgentSource) error {
	if err := agentSourceContext(ctx); err != nil {
		return err
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if entry.Version != 1 || !entry.CreatedAt.Equal(entry.UpdatedAt) {
		return errors.New("new agent sources require version 1 and equal creation and update times")
	}
	document, err := encodeAgentSource(entry)
	if err != nil {
		return err
	}
	err = s.database.Insert(ctx, agentSourcesCollection, document)
	if duplicateAgentSource(err) {
		return agentmodel.ErrAgentSourceExists
	}
	return agentSourceWriteError(err)
}

func (s *AgentSourceMongoStore) Get(ctx context.Context, reference string) (agentmodel.AgentSource, error) {
	document, err := s.find(ctx, reference)
	if err != nil {
		return agentmodel.AgentSource{}, err
	}
	return document.decode()
}

func (s *AgentSourceMongoStore) List(ctx context.Context) ([]agentmodel.AgentSource, error) {
	if err := agentSourceContext(ctx); err != nil {
		return nil, err
	}
	var documents []agentSourceDocument
	err := s.database.Find(ctx, agentSourcesCollection, bson.D{}, &documents,
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetCollation(&options.Collation{Locale: "simple"}))
	if err != nil {
		return nil, err
	}
	entries := make([]agentmodel.AgentSource, 0, len(documents))
	for _, document := range documents {
		entry, err := document.decode()
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *AgentSourceMongoStore) Replace(ctx context.Context, entry agentmodel.AgentSource, expectedVersion uint64) error {
	if err := agentSourceContext(ctx); err != nil {
		return err
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if expectedVersion == 0 || expectedVersion == math.MaxUint64 || entry.Version != expectedVersion+1 {
		return agentmodel.ErrAgentSourceConflict
	}
	current, err := s.find(ctx, entry.Source)
	if err != nil {
		return err
	}
	saved, err := current.decode()
	if err != nil {
		return err
	}
	if saved.Version != expectedVersion || !saved.CreatedAt.Equal(entry.CreatedAt) || entry.UpdatedAt.Before(saved.UpdatedAt) {
		return agentmodel.ErrAgentSourceConflict
	}
	document, err := encodeAgentSource(entry)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Matching the checked payload also binds the write to the exact identity
	// when a deployment has a non-simple default collection collation.
	result, err := s.database.Replace(ctx, agentSourcesCollection, bson.M{
		"_id": entry.Source, "version": current.Version, "data": current.Data,
	}, document, false)
	if err != nil {
		return agentSourceWriteError(err)
	}
	if result.MatchedCount != 1 {
		return agentmodel.ErrAgentSourceConflict
	}
	return nil
}

func (s *AgentSourceMongoStore) find(ctx context.Context, reference string) (agentSourceDocument, error) {
	if err := agentSourceContext(ctx); err != nil {
		return agentSourceDocument{}, err
	}
	if err := agentmodel.ValidateAgentSource(reference); err != nil {
		return agentSourceDocument{}, err
	}
	var document agentSourceDocument
	err := s.database.FindOne(ctx, agentSourcesCollection, bson.M{"_id": reference}, &document,
		options.FindOne().SetCollation(&options.Collation{Locale: "simple"}))
	if errors.Is(err, mongo.ErrNoDocuments) {
		return agentSourceDocument{}, agentmodel.ErrUnknownAgentSource
	}
	return document, err
}

func encodeAgentSource(entry agentmodel.AgentSource) (agentSourceDocument, error) {
	data, err := json.Marshal(entry)
	return agentSourceDocument{ID: entry.Source, Version: strconv.FormatUint(entry.Version, 10), Data: data}, err
}

func (document agentSourceDocument) decode() (agentmodel.AgentSource, error) {
	if len(document.Extra) != 0 {
		return agentmodel.AgentSource{}, errors.New("stored agent source document contains unsupported fields")
	}
	var entry agentmodel.AgentSource
	decoder := json.NewDecoder(bytes.NewReader(document.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entry); err != nil {
		return agentmodel.AgentSource{}, errors.New("invalid stored agent source encoding")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return agentmodel.AgentSource{}, errors.New("stored agent source must contain one JSON value")
	}
	if err := entry.Validate(); err != nil {
		return agentmodel.AgentSource{}, err
	}
	if document.ID != entry.Source || document.Version != strconv.FormatUint(entry.Version, 10) {
		return agentmodel.AgentSource{}, errors.New("stored agent source identity or version does not match indexed fields")
	}
	return entry, nil
}

func duplicateAgentSource(err error) bool {
	var failure mongo.WriteException
	if !errors.As(err, &failure) || len(failure.WriteErrors) != 1 {
		return false
	}
	write := failure.WriteErrors[0]
	if !mongo.IsDuplicateKeyError(write) {
		return false
	}
	pattern, ok := write.Raw.Lookup("keyPattern").DocumentOK()
	if !ok {
		return false
	}
	keys, err := pattern.Elements()
	return err == nil && len(keys) == 1 && keys[0].Key() == "_id"
}

func agentSourceWriteError(err error) error {
	if err == nil {
		return nil
	}
	var failure mongo.WriteException
	if errors.As(err, &failure) && len(failure.WriteErrors) != 0 && failure.WriteConcernError == nil {
		return err
	}
	return errors.Join(agentmodel.ErrAgentSourceWriteUncertain, err)
}

func agentSourceContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("agent source database requires a context")
	}
	return ctx.Err()
}
