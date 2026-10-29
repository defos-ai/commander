package executions

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const logCollection = "execution_logs"

type logDocument struct {
	ID          string          `bson:"_id"`
	Position    int64           `bson:"position"`
	AgentID     string          `bson:"agent_id"`
	ActionName  string          `bson:"action_name"`
	OperationID string          `bson:"operation_id"`
	TaskID      string          `bson:"task_id"`
	Data        json.RawMessage `bson:"data"`
}

func (s *ExecutionMongoStore) AppendLog(ctx context.Context, log executions.Log) error {
	if err := checkLogContext(ctx); err != nil {
		return err
	}
	if err := log.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(log)
	if err != nil {
		return err
	}
	for attempt := range 2 {
		wrote := false
		err := s.database.WithTransaction(ctx, func(tx context.Context) error {
			wrote = false
			previous, err := s.GetLog(tx, log.ID)
			if err == nil {
				if !previous.Equal(log) {
					return executions.ErrLogConflict
				}
				return nil
			}
			if !errors.Is(err, executions.ErrUnknownLog) {
				return err
			}
			head, err := s.readLogHead(tx)
			if err != nil {
				return err
			}
			if head == math.MaxInt64 {
				return errors.New("execution log ingestion position exhausted")
			}
			// The shared counter and insert commit together so a live cursor
			// cannot pass an allocated position before its log is visible.
			position := head + 1
			updated, err := s.database.Update(tx, logCounterCollection, bson.M{"_id": logCounterID, "position": head},
				bson.M{"$set": bson.M{"position": position}})
			if err != nil {
				return err
			}
			if updated.MatchedCount != 1 {
				return errors.New("execution log ingestion counter changed")
			}
			if err := s.database.Insert(tx, logCollection, newLogDocument(log, position, data)); err != nil {
				return err
			}
			wrote = true
			return nil
		})
		// A failed commit confirmation remains uncertain even if a later read
		// could see the inserted log. The caller retains its immutable identity.
		if err == nil || wrote || !mongo.IsDuplicateKeyError(err) {
			return err
		}
		previous, readErr := s.GetLog(ctx, log.ID)
		if readErr == nil {
			if !previous.Equal(log) {
				return executions.ErrLogConflict
			}
			return nil
		}
		if !errors.Is(readErr, executions.ErrUnknownLog) {
			return readErr
		}
		if attempt == 1 {
			return err
		}
	}
	return errors.New("execution log append did not settle")
}

func (s *ExecutionMongoStore) GetLog(ctx context.Context, logID string) (executions.Log, error) {
	if err := checkLogContext(ctx); err != nil {
		return executions.Log{}, err
	}
	if !id.Valid(logID) {
		return executions.Log{}, errors.New("execution log ID must be a canonical UUID")
	}
	var document logDocument
	if err := s.database.GetByID(ctx, logCollection, logID, &document); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			err = executions.ErrUnknownLog
		}
		return executions.Log{}, err
	}
	return document.decode()
}

func (document logDocument) decode() (executions.Log, error) {
	var log executions.Log
	if !utf8.Valid(document.Data) || domain.Decode(document.Data, &log) != nil {
		return executions.Log{}, errors.New("invalid stored execution log encoding")
	}
	if document.Position <= 0 {
		return executions.Log{}, errors.New("stored execution log has no valid ingestion position")
	}
	if log.ID != document.ID || log.AgentID != document.AgentID || log.ActionName != document.ActionName ||
		log.Work.OperationID != document.OperationID || log.Work.TaskID != document.TaskID {
		return executions.Log{}, errors.New("stored execution log does not match its indexed metadata")
	}
	return log, nil
}

func newLogDocument(log executions.Log, position int64, data json.RawMessage) logDocument {
	return logDocument{ID: log.ID, Position: position, AgentID: log.AgentID, ActionName: log.ActionName,
		OperationID: log.Work.OperationID, TaskID: log.Work.TaskID, Data: data}
}

func checkLogContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("execution log database requires a context")
	}
	return ctx.Err()
}
