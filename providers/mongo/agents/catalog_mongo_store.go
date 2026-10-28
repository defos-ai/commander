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

const catalogCollection = "agent_catalog"

type CatalogMongoStore struct{ database *store.MongoStore }

var _ agentmodel.CatalogStore = (*CatalogMongoStore)(nil)

// JSON retains nanosecond timestamps and all uint64 versions. The indexed
// version is decimal text so BSON's signed integer range cannot truncate it.
type catalogDocument struct {
	ID      string          `bson:"_id"`
	Version string          `bson:"version"`
	Data    json.RawMessage `bson:"data"`
	Extra   bson.M          `bson:",inline"`
}

func NewCatalogMongoStore(database *store.MongoStore) *CatalogMongoStore {
	return &CatalogMongoStore{database: database}
}

func (s *CatalogMongoStore) Create(ctx context.Context, entry agentmodel.CatalogEntry) error {
	if err := catalogContext(ctx); err != nil {
		return err
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if entry.Version != 1 || !entry.CreatedAt.Equal(entry.UpdatedAt) {
		return errors.New("new catalog entries require version 1 and equal creation and update times")
	}
	document, err := encodeCatalogEntry(entry)
	if err != nil {
		return err
	}
	err = s.database.Insert(ctx, catalogCollection, document)
	if duplicateCatalogPackage(err) {
		return agentmodel.ErrCatalogEntryExists
	}
	return catalogWriteError(err)
}

func (s *CatalogMongoStore) Get(ctx context.Context, reference string) (agentmodel.CatalogEntry, error) {
	document, err := s.find(ctx, reference)
	if err != nil {
		return agentmodel.CatalogEntry{}, err
	}
	return document.decode()
}

func (s *CatalogMongoStore) List(ctx context.Context) ([]agentmodel.CatalogEntry, error) {
	if err := catalogContext(ctx); err != nil {
		return nil, err
	}
	var documents []catalogDocument
	err := s.database.Find(ctx, catalogCollection, bson.D{}, &documents,
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetCollation(&options.Collation{Locale: "simple"}))
	if err != nil {
		return nil, err
	}
	entries := make([]agentmodel.CatalogEntry, 0, len(documents))
	for _, document := range documents {
		entry, err := document.decode()
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *CatalogMongoStore) Replace(ctx context.Context, entry agentmodel.CatalogEntry, expectedVersion uint64) error {
	if err := catalogContext(ctx); err != nil {
		return err
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if expectedVersion == 0 || expectedVersion == math.MaxUint64 || entry.Version != expectedVersion+1 {
		return agentmodel.ErrCatalogConflict
	}
	current, err := s.find(ctx, entry.Package)
	if err != nil {
		return err
	}
	saved, err := current.decode()
	if err != nil {
		return err
	}
	if saved.Version != expectedVersion || !saved.CreatedAt.Equal(entry.CreatedAt) || entry.UpdatedAt.Before(saved.UpdatedAt) {
		return agentmodel.ErrCatalogConflict
	}
	document, err := encodeCatalogEntry(entry)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Matching the checked payload also binds the write to the exact identity
	// when a deployment has a non-simple default collection collation.
	result, err := s.database.Replace(ctx, catalogCollection, bson.M{
		"_id": entry.Package, "version": current.Version, "data": current.Data,
	}, document, false)
	if err != nil {
		return catalogWriteError(err)
	}
	if result.MatchedCount != 1 {
		return agentmodel.ErrCatalogConflict
	}
	return nil
}

func (s *CatalogMongoStore) find(ctx context.Context, reference string) (catalogDocument, error) {
	if err := catalogContext(ctx); err != nil {
		return catalogDocument{}, err
	}
	if err := agentmodel.ValidateCatalogPackage(reference); err != nil {
		return catalogDocument{}, err
	}
	var document catalogDocument
	err := s.database.FindOne(ctx, catalogCollection, bson.M{"_id": reference}, &document,
		options.FindOne().SetCollation(&options.Collation{Locale: "simple"}))
	if errors.Is(err, mongo.ErrNoDocuments) {
		return catalogDocument{}, agentmodel.ErrUnknownCatalogEntry
	}
	return document, err
}

func encodeCatalogEntry(entry agentmodel.CatalogEntry) (catalogDocument, error) {
	data, err := json.Marshal(entry)
	return catalogDocument{ID: entry.Package, Version: strconv.FormatUint(entry.Version, 10), Data: data}, err
}

func (document catalogDocument) decode() (agentmodel.CatalogEntry, error) {
	if len(document.Extra) != 0 {
		return agentmodel.CatalogEntry{}, errors.New("stored catalog document contains unsupported fields")
	}
	var entry agentmodel.CatalogEntry
	decoder := json.NewDecoder(bytes.NewReader(document.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entry); err != nil {
		return agentmodel.CatalogEntry{}, errors.New("invalid stored catalog entry encoding")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return agentmodel.CatalogEntry{}, errors.New("stored catalog entry must contain one JSON value")
	}
	if err := entry.Validate(); err != nil {
		return agentmodel.CatalogEntry{}, err
	}
	if document.ID != entry.Package || document.Version != strconv.FormatUint(entry.Version, 10) {
		return agentmodel.CatalogEntry{}, errors.New("stored catalog identity or version does not match indexed fields")
	}
	return entry, nil
}

func duplicateCatalogPackage(err error) bool {
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

func catalogWriteError(err error) error {
	if err == nil {
		return nil
	}
	var failure mongo.WriteException
	if errors.As(err, &failure) && len(failure.WriteErrors) != 0 && failure.WriteConcernError == nil {
		return err
	}
	return errors.Join(agentmodel.ErrCatalogWriteUncertain, err)
}

func catalogContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("catalog database requires a context")
	}
	return ctx.Err()
}
