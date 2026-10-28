package executions

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const logCounterCollection = "execution_log_counters"
const logCounterID = "global"

func (s *ExecutionMongoStore) initializeLogs(ctx context.Context) error {
	var invalid []struct{ ID string }
	filter := bson.M{"$nor": bson.A{bson.M{"position": bson.M{"$gt": int64(0), "$type": bson.A{"int", "long"}}}}}
	if err := s.database.Find(ctx, logCollection, filter, &invalid,
		options.Find().SetLimit(1).SetProjection(bson.M{"_id": 1})); err != nil {
		return err
	}
	if len(invalid) != 0 {
		return errors.New("existing execution logs lack valid ingestion positions; automatic backfill is not supported")
	}
	indexes := []mongo.IndexModel{{Keys: bson.D{{Key: "position", Value: 1}},
		Options: options.Index().SetName("execution_log_position").SetUnique(true).SetCollation(&options.Collation{Locale: "simple"})}}
	for _, field := range []string{"operation_id", "task_id", "agent_id", "action_name"} {
		indexes = append(indexes, mongo.IndexModel{Keys: bson.D{{Key: field, Value: 1}, {Key: "position", Value: 1}},
			Options: options.Index().SetName("execution_log_" + field + "_position").SetCollation(&options.Collation{Locale: "simple"})})
	}
	if err := s.database.EnsureIndexes(ctx, logCollection, indexes); err != nil {
		return err
	}
	_, err := s.database.Upsert(ctx, logCounterCollection, bson.M{"_id": logCounterID}, bson.M{"$setOnInsert": bson.M{"position": int64(0)}})
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return err
	}
	return s.database.WithTransaction(ctx, func(tx context.Context) error {
		head, err := s.readLogHead(tx)
		if err != nil {
			return err
		}
		var latest []struct{ Position int64 }
		if err := s.database.Find(tx, logCollection, bson.D{}, &latest,
			options.Find().SetLimit(1).SetSort(bson.D{{Key: "position", Value: -1}}).SetProjection(bson.M{"position": 1})); err != nil {
			return err
		}
		last := int64(0)
		if len(latest) != 0 {
			last = latest[0].Position
		}
		if last != head {
			return errors.New("execution log ingestion counter does not match saved logs")
		}
		return nil
	})
}

func (s *ExecutionMongoStore) readLogHead(ctx context.Context) (int64, error) {
	var counter struct {
		ID       string `bson:"_id"`
		Position *int64 `bson:"position"`
	}
	if err := s.database.GetByID(ctx, logCounterCollection, logCounterID, &counter); err != nil {
		return 0, err
	}
	if counter.ID != logCounterID || counter.Position == nil || *counter.Position < 0 {
		return 0, errors.New("invalid execution log ingestion counter")
	}
	return *counter.Position, nil
}
