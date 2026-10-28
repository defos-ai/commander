package executions

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

const logCollection = "execution_logs"

type logDocument struct {
	ID   string          `bson:"_id"`
	Data json.RawMessage `bson:"data"`
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
	err = s.database.Insert(ctx, logCollection, logDocument{ID: log.ID, Data: data})
	if !mongo.IsDuplicateKeyError(err) {
		return err
	}
	saved, err := s.GetLog(ctx, log.ID)
	if err != nil {
		return err
	}
	if !saved.Equal(log) {
		return executions.ErrLogConflict
	}
	return nil
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
	if log.ID != document.ID {
		return executions.Log{}, errors.New("stored execution log ID does not match its document")
	}
	return log, nil
}

func checkLogContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("execution log database requires a context")
	}
	return ctx.Err()
}
