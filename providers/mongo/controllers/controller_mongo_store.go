package controllers

import (
	"context"
	"errors"

	controllermodel "commander/framework/domain/controllers"
	"commander/framework/id"
	"commander/providers/mongo/internal/bsonvalue"
	"commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const collection = "controllers"

type ControllerMongoStore struct{ database *store.MongoStore }

var _ controllermodel.ControllerStore = (*ControllerMongoStore)(nil)

func NewControllerMongoStore(ctx context.Context, database *store.MongoStore) (*ControllerMongoStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if database == nil {
		return nil, errors.New("controller store requires a MongoDB store")
	}
	// MongoDB's built-in _id index uniquely identifies each runtime. No TTL
	// index is used: expired controllers remain available for diagnosis.
	return &ControllerMongoStore{database: database}, nil
}

func (s *ControllerMongoStore) Upsert(ctx context.Context, record controllermodel.Controller) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	document := encode(record)
	heartbeat := bsonvalue.NewTimestamp(record.LastHeartbeat)
	filter := bson.M{"_id": record.ID, "$or": bson.A{
		bson.M{"last_heartbeat.time": bson.M{"$lt": heartbeat.Time}},
		bson.M{"last_heartbeat.time": heartbeat.Time, "last_heartbeat.nanoseconds": bson.M{"$lt": heartbeat.Nanoseconds}},
	}}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := s.database.Replace(ctx, collection, filter, document, true)
		if !duplicateControllerID(err) {
			return err
		}
		// A stale observation tries to insert an existing _id. Another writer
		// may have inserted an older heartbeat, so read before classifying it.
		saved, err := s.Get(ctx, record.ID)
		if errors.Is(err, controllermodel.ErrUnknownController) {
			continue
		}
		if err != nil {
			return err
		}
		if !record.LastHeartbeat.After(saved.LastHeartbeat) {
			return controllermodel.ErrStaleObservation
		}
		// Retain the timestamp predicate when retrying after an insert race.
		result, err := s.database.Replace(ctx, collection, filter, document, false)
		if err != nil {
			return err
		}
		if result.MatchedCount != 0 {
			return nil
		}
	}
}

func (s *ControllerMongoStore) Get(ctx context.Context, controllerID string) (controllermodel.Controller, error) {
	if err := ctx.Err(); err != nil {
		return controllermodel.Controller{}, err
	}
	if !id.ValidRuntime(controllerID, id.Controller) {
		return controllermodel.Controller{}, controllermodel.ErrUnknownController
	}
	var document controllerDocument
	if err := s.database.GetByID(ctx, collection, controllerID, &document); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return controllermodel.Controller{}, controllermodel.ErrUnknownController
		}
		return controllermodel.Controller{}, err
	}
	return document.decode()
}

func (s *ControllerMongoStore) List(ctx context.Context) ([]controllermodel.Controller, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var documents []controllerDocument
	if err := s.database.Find(ctx, collection, bson.M{}, &documents, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})); err != nil {
		return nil, err
	}
	records := make([]controllermodel.Controller, 0, len(documents))
	for _, document := range documents {
		record, err := document.decode()
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// A conflict on any additional unique index is a storage failure, not an
// observation-ordering conflict. Only the primary key can trigger a retry.
func duplicateControllerID(err error) bool {
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
