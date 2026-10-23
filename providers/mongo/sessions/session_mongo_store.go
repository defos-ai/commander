package sessions

import (
	"context"
	"encoding/json"
	"errors"

	model "github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/mongo/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const collection = "runtime_sessions"

type SessionMongoStore struct{ database *store.MongoStore }
type document struct {
	ID      string `bson:"_id"`
	Payload []byte `bson:"payload"`
}

var _ model.Store = (*SessionMongoStore)(nil)

func NewSessionMongoStore(ctx context.Context, database *store.MongoStore) (*SessionMongoStore, error) {
	// Establish the collection before transactions; no TTL may remove retained identities.
	if err := database.EnsureIndexes(ctx, collection, []mongo.IndexModel{{Keys: bson.D{{Key: "_id", Value: 1}}}}); err != nil {
		return nil, err
	}
	return &SessionMongoStore{database}, nil
}
func (d document) decode() (model.Scope, error) {
	var s model.Scope
	if err := json.Unmarshal(d.Payload, &s); err != nil {
		return s, err
	}
	if s.ID != d.ID {
		return s, model.ErrConflict
	}
	return s, s.Validate()
}
func (s *SessionMongoStore) Get(ctx context.Context, scopeID string) (model.Scope, error) {
	if !id.Valid(scopeID) {
		return model.Scope{}, model.ErrConflict
	}
	var d document
	err := s.database.GetByID(ctx, collection, scopeID, &d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return model.Scope{ID: scopeID, Records: []model.Record{}}, nil
	}
	if err != nil {
		return model.Scope{}, err
	}
	return d.decode()
}
func (s *SessionMongoStore) List(ctx context.Context) ([]model.Scope, error) {
	var docs []document
	if err := s.database.Find(ctx, collection, bson.M{}, &docs); err != nil {
		return nil, err
	}
	result := make([]model.Scope, 0, len(docs))
	for _, d := range docs {
		r, err := d.decode()
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}
func (s *SessionMongoStore) Apply(ctx context.Context, c model.Change) (model.Record, error) {
	var result model.Record
	wrote := false
	err := s.database.WithTransaction(ctx, func(tx context.Context) error {
		wrote = false
		scope, err := s.Get(tx, c.ScopeID)
		if err != nil {
			return err
		}
		if err := scope.Apply(c); err != nil {
			return err
		}
		data, err := json.Marshal(scope)
		if err != nil {
			return err
		}
		result, err = scope.Find(c.ID)
		if err != nil {
			return err
		}
		wrote = true
		_, err = s.database.Replace(tx, collection, bson.M{"_id": c.ScopeID}, document{ID: c.ScopeID, Payload: data}, true)
		return err
	})
	if err != nil && wrote {
		return result, errors.Join(model.ErrWriteUncertain, err)
	}
	return result, err
}
