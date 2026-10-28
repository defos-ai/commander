package executions

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/executions"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (s *ExecutionMongoStore) ListLogs(ctx context.Context, query executions.LogQuery) ([]executions.Log, error) {
	if err := checkLogContext(ctx); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	var logs []executions.Log
	err := s.database.WithTransaction(ctx, func(tx context.Context) error {
		logs = []executions.Log{}
		head, err := s.readLogHead(tx)
		if err != nil {
			return err
		}
		var after int64
		if query.AfterLogID != "" {
			var anchor logDocument
			if err := s.database.GetByID(tx, logCollection, query.AfterLogID, &anchor); err != nil {
				if errors.Is(err, mongo.ErrNoDocuments) {
					return executions.ErrUnknownLog
				}
				return err
			}
			log, err := anchor.decode()
			if err != nil {
				return err
			}
			if anchor.Position > head || !query.Matches(log) {
				return executions.ErrUnknownLog
			}
			after = anchor.Position
		}
		if after >= head {
			return nil
		}
		filter := bson.M{"position": bson.M{"$gt": after, "$lte": head}}
		for _, field := range []struct{ name, value string }{
			{"operation_id", query.OperationID}, {"task_id", query.TaskID}, {"agent_id", query.AgentID}, {"action_name", query.ActionName},
		} {
			if field.value != "" {
				filter[field.name] = field.value
			}
		}
		var documents []logDocument
		if err := s.database.Find(tx, logCollection, filter, &documents,
			options.Find().SetSort(bson.D{{Key: "position", Value: 1}}).SetLimit(int64(query.Limit)).
				SetCollation(&options.Collation{Locale: "simple"})); err != nil {
			return err
		}
		last := after
		for _, document := range documents {
			log, err := document.decode()
			if err != nil {
				return err
			}
			if document.Position <= last || document.Position > head || !query.Matches(log) {
				return errors.New("stored execution log does not match its query position or filters")
			}
			logs = append(logs, log)
			last = document.Position
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return logs, nil
}
