package executions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/mongo/internal/bsonvalue"
	"github.com/defos-ai/commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const collection = "executions"

type ExecutionMongoStore struct {
	database *store.MongoStore
}

var _ executions.ExecutionStore = (*ExecutionMongoStore)(nil)

func NewExecutionMongoStore(ctx context.Context, database *store.MongoStore) (*ExecutionMongoStore, error) {
	if ctx == nil {
		return nil, errors.New("execution database requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if database == nil {
		return nil, errors.New("execution database is required")
	}
	result := &ExecutionMongoStore{database: database}
	if err := result.initializeLogs(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *ExecutionMongoStore) Claim(ctx context.Context, command messages.Message) (executions.Execution, bool, error) {
	key, err := executionID(ctx, command.AgentID, command.ID)
	if err != nil {
		return executions.Execution{}, false, err
	}
	candidate := executions.Execution{Command: command, StartedAt: time.Now().UTC()}
	if err := candidate.Validate(); err != nil {
		return executions.Execution{}, false, err
	}
	document := executionDocument{ID: key, Command: encodeMessage(command), StartedAt: bsonvalue.NewTimestamp(candidate.StartedAt)}
	err = s.database.Insert(ctx, collection, document)
	if err == nil {
		record, err := document.decode()
		return record, err == nil, err
	}
	if !mongo.IsDuplicateKeyError(err) {
		return executions.Execution{}, false, err
	}
	record, err := s.Get(ctx, command.AgentID, command.ID)
	if err != nil {
		return executions.Execution{}, false, err
	}
	if !sameExecutionMessage(record.Command, command) {
		return executions.Execution{}, false, executions.ErrExecutionConflict
	}
	return record, false, nil
}

func (s *ExecutionMongoStore) Complete(ctx context.Context, result messages.Message) error {
	key, err := executionID(ctx, result.AgentID, result.CorrelationID)
	if err != nil {
		return err
	}
	if err := result.Validate(); err != nil {
		return err
	}
	if result.Type != messages.ActionSucceeded && result.Type != messages.CommandFailed {
		return errors.New("execution result must indicate success or failure")
	}
	record, err := s.Get(ctx, result.AgentID, result.CorrelationID)
	if err != nil {
		return err
	}
	if record.Result != nil {
		return compareResult(record, result)
	}
	completed := time.Now().UTC()
	record.Result, record.CompletedAt = &result, &completed
	if err := record.Validate(); err != nil {
		return err
	}
	fields := bson.M{"result": encodeMessage(result), "completed_at": bsonvalue.NewTimestamp(completed)}
	updated, err := s.database.Update(ctx, collection,
		bson.M{"_id": key, "result": nil, "completed_at": nil}, bson.M{"$set": fields})
	if err != nil {
		return err
	}
	if updated.MatchedCount == 1 {
		return nil
	}
	winner, err := s.Get(ctx, result.AgentID, result.CorrelationID)
	if err != nil {
		return err
	}
	return compareResult(winner, result)
}

func (s *ExecutionMongoStore) SaveCheckpoint(ctx context.Context, agentID, commandID, name string, data json.RawMessage) error {
	key, err := executionID(ctx, agentID, commandID)
	if err != nil {
		return err
	}
	if err := executions.ValidateCheckpoint(name, data); err != nil {
		return err
	}
	// Read and update the whole map in one transaction. A concurrent checkpoint
	// or completion forces a fresh snapshot, preserving names and terminal state.
	return s.database.WithTransaction(ctx, func(tx context.Context) error {
		record, err := s.Get(tx, agentID, commandID)
		if err != nil {
			return err
		}
		created, err := record.RecordCheckpoint(name, data)
		if err != nil || !created {
			return err
		}
		updated, err := s.database.Update(tx, collection, bson.M{"_id": key},
			bson.M{"$set": bson.M{"checkpoints": record.Checkpoints}})
		if err != nil {
			return err
		}
		if updated.MatchedCount != 1 {
			return executions.ErrUnknownExecution
		}
		return nil
	})
}

func (s *ExecutionMongoStore) Get(ctx context.Context, agentID, commandID string) (executions.Execution, error) {
	key, err := executionID(ctx, agentID, commandID)
	if err != nil {
		return executions.Execution{}, err
	}
	var document executionDocument
	if err := s.database.GetByID(ctx, collection, key, &document); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return executions.Execution{}, executions.ErrUnknownExecution
		}
		return executions.Execution{}, err
	}
	return document.decode()
}

func executionID(ctx context.Context, agentID, commandID string) (string, error) {
	if ctx == nil {
		return "", errors.New("execution database requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !id.ValidAgent(agentID) || !id.Valid(commandID) {
		return "", errors.New("execution requires a valid agent ID and a canonical UUID command ID")
	}
	return agentID + "/" + commandID, nil
}

func compareResult(record executions.Execution, result messages.Message) error {
	if record.Result == nil || !sameExecutionMessage(*record.Result, result) {
		return executions.ErrExecutionConflict
	}
	return nil
}

func sameExecutionMessage(a, b messages.Message) bool {
	if a.ID != b.ID || a.Type != b.Type || a.AgentID != b.AgentID || a.CorrelationID != b.CorrelationID || a.Work != b.Work {
		return false
	}
	var first, second any
	decode := func(payload json.RawMessage, target *any) error {
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		return decoder.Decode(target)
	}
	return decode(a.Payload, &first) == nil && decode(b.Payload, &second) == nil && reflect.DeepEqual(first, second)
}
