package operations

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (s *OperationMongoStore) GetTask(ctx context.Context, operationID, taskID string) (operations.Task, error) {
	if err := checkOperationID(ctx, operationID); err != nil {
		return operations.Task{}, err
	}
	if err := validateRecordID(taskID); err != nil {
		return operations.Task{}, err
	}
	var task operations.Task
	err := s.database.WithTransaction(ctx, func(tx context.Context) error {
		metadata, err := s.readMetadata(tx, operationID)
		if err != nil {
			return err
		}
		doc, err := readTaskRecord(tx, s.database, metadata, taskID, false)
		if err != nil {
			return err
		}
		task, err = doc.task()
		return err
	})
	if err != nil {
		return operations.Task{}, err
	}
	return task, nil
}

func (s *OperationMongoStore) ListTasks(ctx context.Context, operationID, afterTaskID string, limit int) ([]operations.Task, error) {
	if err := checkOperationID(ctx, operationID); err != nil {
		return nil, err
	}
	if err := domain.ValidatePageLimit(limit); err != nil {
		return nil, err
	}
	if afterTaskID != "" {
		if err := validateRecordID(afterTaskID); err != nil {
			return nil, err
		}
	}
	var tasks []operations.Task
	err := s.database.WithTransaction(ctx, func(tx context.Context) error {
		metadata, err := s.readMetadata(tx, operationID)
		if err != nil {
			return err
		}
		var position int64
		if afterTaskID != "" {
			doc, err := readTaskRecord(tx, s.database, metadata, afterTaskID, true)
			if err != nil {
				return err
			}
			position = doc.Position
		}
		var docs []recordDocument
		filter := bson.M{"operation_id": operationID, "position": bson.M{"$gt": position, "$lte": metadata.TaskCount}}
		if err := s.database.Find(tx, tasksCollection, filter, &docs,
			options.Find().SetSort(bson.D{{Key: "position", Value: 1}}).SetLimit(int64(limit))); err != nil {
			return err
		}
		if int64(len(docs)) != min(int64(limit), metadata.TaskCount-position) {
			return errors.New("task page does not match operation metadata")
		}
		tasks = make([]operations.Task, 0, len(docs))
		for i, doc := range docs {
			if err := doc.validatePosition(operationID, position+int64(i)+1); err != nil {
				return err
			}
			task, err := doc.task()
			if err != nil {
				return err
			}
			tasks = append(tasks, task)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *OperationMongoStore) ListEvents(ctx context.Context, operationID string, afterSequence uint64, limit int) ([]operations.Event, error) {
	if err := checkOperationID(ctx, operationID); err != nil {
		return nil, err
	}
	if err := domain.ValidatePageLimit(limit); err != nil {
		return nil, err
	}
	var events []operations.Event
	err := s.database.WithTransaction(ctx, func(tx context.Context) error {
		metadata, err := s.readMetadata(tx, operationID)
		if err != nil {
			return err
		}
		var docs []recordDocument
		filter := bson.M{"operation_id": operationID, "key": bson.M{"$gt": eventKey(afterSequence)}}
		if err := s.database.Find(tx, eventsCollection, filter, &docs, options.Find().
			SetSort(bson.D{{Key: "key", Value: 1}}).SetLimit(int64(limit)).SetCollation(&options.Collation{Locale: "simple"})); err != nil {
			return err
		}
		events = make([]operations.Event, 0, len(docs))
		for _, doc := range docs {
			if doc.Position > metadata.EventCount {
				return errors.New("event position exceeds operation metadata")
			}
			if err := doc.validatePosition(operationID, doc.Position); err != nil {
				return err
			}
			event, err := doc.event()
			if err != nil {
				return err
			}
			events = append(events, event)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

func readTaskRecord(ctx context.Context, database *store.MongoStore, metadata operationDocument, taskID string, positionOnly bool) (recordDocument, error) {
	var doc recordDocument
	query := options.FindOne()
	if positionOnly {
		query.SetProjection(bson.M{"data": 0})
	}
	err := database.FindOne(ctx, tasksCollection, bson.M{"_id": recordID(metadata.ID, taskID)}, &doc, query)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return doc, operations.ErrUnknownTask
	}
	if err != nil {
		return doc, err
	}
	if doc.Key != taskID || doc.Position > metadata.TaskCount {
		return doc, errors.New("task identity or position does not match operation metadata")
	}
	return doc, doc.validatePosition(metadata.ID, doc.Position)
}
