package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const collection = "actions"

type ActionMongoStore struct {
	database *store.MongoStore
}

var _ actions.ActionStore = (*ActionMongoStore)(nil)

// Raw JSON schemas remain BSON binary values to preserve their exact content.
type actionDocument struct {
	ID     string         `bson:"_id"`
	Action actions.Action `bson:",inline"`
}

func NewActionMongoStore(database *store.MongoStore) *ActionMongoStore {
	return &ActionMongoStore{database: database}
}

func (s *ActionMongoStore) Upsert(ctx context.Context, action actions.Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateCatalogAction(action); err != nil {
		return err
	}
	_, err := s.database.Replace(ctx, collection, bson.M{"_id": action.Name}, actionDocument{ID: action.Name, Action: action}, true)
	return err
}

func (s *ActionMongoStore) Get(ctx context.Context, name string) (actions.Action, error) {
	var document actionDocument
	if err := s.database.GetByID(ctx, collection, name, &document); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return actions.Action{}, actions.ErrUnknownAction
		}
		return actions.Action{}, err
	}
	return document.decode()
}

func (s *ActionMongoStore) List(ctx context.Context) ([]actions.Action, error) {
	var documents []actionDocument
	if err := s.database.Find(ctx, collection, bson.D{}, &documents, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})); err != nil {
		return nil, err
	}
	result := make([]actions.Action, 0, len(documents))
	for _, document := range documents {
		action, err := document.decode()
		if err != nil {
			return nil, err
		}
		result = append(result, action)
	}
	return result, nil
}

func (d actionDocument) decode() (actions.Action, error) {
	if d.ID != d.Action.Name {
		return actions.Action{}, errors.New("stored action ID does not match its name")
	}
	if err := validateCatalogAction(d.Action); err != nil {
		return actions.Action{}, fmt.Errorf("invalid stored action %q: %w", d.ID, err)
	}
	return d.Action, nil
}

func validateCatalogAction(action actions.Action) error {
	if action.Status != "" && action.Status != actions.Unknown {
		return errors.New("action presence belongs to an agent, not the action catalog")
	}
	return action.Validate()
}
