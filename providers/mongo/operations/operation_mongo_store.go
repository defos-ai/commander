package operations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"commander/framework/domain/operations"
	"commander/framework/id"
	"commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type OperationMongoStore struct{ database *store.MongoStore }

var _ operations.OperationStore = (*OperationMongoStore)(nil)

func NewOperationMongoStore(ctx context.Context, database *store.MongoStore) (*OperationMongoStore, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	// Create every collection before the first transaction; indexes also enforce
	// one stable position and one record identity within each operation.
	if err := database.EnsureIndexes(ctx, collection, []mongo.IndexModel{{Keys: bson.D{{Key: "status", Value: 1}}}}); err != nil {
		return nil, err
	}
	for _, name := range []string{tasksCollection, questionsCollection, eventsCollection} {
		indexes := []mongo.IndexModel{{Keys: bson.D{{Key: "operation_id", Value: 1}, {Key: "position", Value: 1}}, Options: options.Index().SetUnique(true)}}
		if name == eventsCollection {
			indexes = append(indexes, mongo.IndexModel{Keys: bson.D{{Key: "operation_id", Value: 1}, {Key: "key", Value: 1}}, Options: options.Index().SetUnique(true).SetCollation(&options.Collation{Locale: "simple"})})
		}
		if err := database.EnsureIndexes(ctx, name, indexes); err != nil {
			return nil, err
		}
	}
	return &OperationMongoStore{database: database}, nil
}

func (s *OperationMongoStore) Create(ctx context.Context, op operations.Operation) error {
	if err := checkOperationID(ctx, op.ID); err != nil {
		return err
	}
	records, err := encodeOperation(op.Clone())
	if err != nil {
		return err
	}
	records.metadata.Revision = 1
	wrote := false
	err = s.database.WithTransaction(ctx, func(tx context.Context) error {
		wrote = false
		if err := s.database.Insert(tx, collection, records.metadata); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return operations.ErrOperationConflict
			}
			return err
		}
		for _, group := range recordGroups(records) {
			for _, record := range group.records {
				if err := s.database.Insert(tx, group.collection, record); err != nil {
					return err
				}
			}
		}
		wrote = true
		return nil
	})
	return operationWriteError(op.ID, wrote, err)
}

func (s *OperationMongoStore) Get(ctx context.Context, operationID string) (operations.Operation, error) {
	if err := checkOperationID(ctx, operationID); err != nil {
		return operations.Operation{}, err
	}
	var result operations.Operation
	err := s.database.WithTransaction(ctx, func(tx context.Context) error {
		records, err := s.readOperation(tx, operationID)
		if err != nil {
			return err
		}
		result, err = records.decode()
		return err
	})
	if err != nil {
		return operations.Operation{}, err
	}
	return result, nil
}

func (s *OperationMongoStore) List(ctx context.Context) ([]operations.Operation, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	var documents []struct {
		ID string `bson:"_id"`
	}
	if err := s.database.Find(ctx, collection, bson.D{}, &documents, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetProjection(bson.M{"_id": 1})); err != nil {
		return nil, err
	}
	result := make([]operations.Operation, 0, len(documents))
	for _, document := range documents {
		op, err := s.Get(ctx, document.ID)
		if errors.Is(err, operations.ErrUnknownOperation) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, op)
	}
	return result, nil
}

func (s *OperationMongoStore) update(ctx context.Context, operationID string, mutate func(*operations.Operation) error) (operations.Operation, error) {
	if err := checkOperationID(ctx, operationID); err != nil {
		return operations.Operation{}, err
	}
	var result operations.Operation
	wrote := false
	err := s.database.WithTransaction(ctx, func(tx context.Context) error {
		// Transaction retries must reconstruct everything from the latest snapshot,
		// including generated child IDs and event sequences.
		wrote, result = false, operations.Operation{}
		previous, err := s.readOperation(tx, operationID)
		if err != nil {
			return err
		}
		current, err := previous.decode()
		if err != nil {
			return err
		}
		next := current.Clone()
		if err := mutate(&next); err != nil {
			return err
		}
		if next.ID != operationID {
			return errors.New("operation identity cannot change")
		}
		encoded, err := encodeOperation(next)
		if err != nil {
			return err
		}
		if sameRecords(previous, encoded) {
			result = current
			return nil
		}
		if previous.metadata.Revision == math.MaxInt64 {
			return errors.New("operation storage revision exhausted")
		}
		encoded.metadata.Revision = previous.metadata.Revision + 1
		// All mutations contend on this operation alone. This prevents decisions
		// based on stale questions/preflight even when they change different tasks.
		updated, err := s.database.Replace(tx, collection, bson.M{"_id": operationID, "revision": previous.metadata.Revision}, encoded.metadata, false)
		if err != nil {
			return err
		}
		if updated.MatchedCount != 1 {
			return operations.ErrOperationConflict
		}
		before := recordGroups(previous)
		for i, after := range recordGroups(encoded) {
			if err := s.writeChanges(tx, after.collection, before[i].records, after.records); err != nil {
				return err
			}
		}
		result, wrote = next.Clone(), true
		return nil
	})
	if err = operationWriteError(operationID, wrote, err); err != nil {
		if errors.Is(err, operations.ErrWriteUncertain) {
			return result, err
		}
		return operations.Operation{}, err
	}
	return result, nil
}

func (s *OperationMongoStore) readOperation(ctx context.Context, operationID string) (operationRecords, error) {
	metadata, err := s.readMetadata(ctx, operationID)
	if err != nil {
		return operationRecords{}, err
	}
	records := operationRecords{metadata: metadata}
	for _, group := range []struct {
		name string
		out  *[]recordDocument
	}{
		{tasksCollection, &records.tasks}, {questionsCollection, &records.questions}, {eventsCollection, &records.events},
	} {
		if err := s.database.Find(ctx, group.name, bson.M{"operation_id": operationID}, group.out, options.Find().SetSort(bson.D{{Key: "position", Value: 1}})); err != nil {
			return operationRecords{}, err
		}
	}
	return records, nil
}

func (s *OperationMongoStore) readMetadata(ctx context.Context, operationID string) (operationDocument, error) {
	var metadata operationDocument
	if err := s.database.GetByID(ctx, collection, operationID, &metadata); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return metadata, operations.ErrUnknownOperation
		}
		return metadata, err
	}
	_, err := metadata.decode()
	return metadata, err
}

func (s *OperationMongoStore) writeChanges(ctx context.Context, name string, previous, next []recordDocument) error {
	old := make(map[string]recordDocument, len(previous))
	for _, doc := range previous {
		old[doc.ID] = doc
	}
	for _, doc := range next {
		before, exists := old[doc.ID]
		delete(old, doc.ID)
		if !exists {
			if err := s.database.Insert(ctx, name, doc); err != nil {
				return err
			}
		} else if before.Position != doc.Position || !bytes.Equal(before.Data, doc.Data) {
			result, err := s.database.Replace(ctx, name, bson.M{"_id": doc.ID}, doc, false)
			if err != nil {
				return err
			}
			if result.MatchedCount != 1 {
				return errors.New("operation record disappeared during update")
			}
		}
	}
	for key := range old {
		if _, err := s.database.Delete(ctx, name, bson.M{"_id": key}); err != nil {
			return err
		}
	}
	return nil
}

type recordGroup struct {
	collection string
	records    []recordDocument
}

func recordGroups(r operationRecords) []recordGroup {
	return []recordGroup{{tasksCollection, r.tasks}, {questionsCollection, r.questions}, {eventsCollection, r.events}}
}

func sameRecords(a, b operationRecords) bool {
	if !bytes.Equal(a.metadata.Data, b.metadata.Data) || a.metadata.TaskCount != b.metadata.TaskCount || a.metadata.QuestionCount != b.metadata.QuestionCount || a.metadata.EventCount != b.metadata.EventCount {
		return false
	}
	before := recordGroups(a)
	for i, after := range recordGroups(b) {
		if len(before[i].records) != len(after.records) {
			return false
		}
		for j, record := range after.records {
			other := before[i].records[j]
			if record.ID != other.ID || record.Position != other.Position || !bytes.Equal(record.Data, other.Data) {
				return false
			}
		}
	}
	return true
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("operation database requires a context")
	}
	return ctx.Err()
}
func checkOperationID(ctx context.Context, operationID string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if !id.Valid(operationID) {
		return errors.New("operation ID must be a canonical UUID")
	}
	return nil
}
func operationWriteError(operationID string, wrote bool, err error) error {
	// An error after the last callback wrote successfully may be a lost commit
	// confirmation. Pause the runner even if visibility cannot yet be established.
	if err != nil && wrote {
		return fmt.Errorf("commit operation %s: %w", operationID, errors.Join(operations.ErrWriteUncertain, err))
	}
	return err
}
