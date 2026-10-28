package executions

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/executions"
)

func (s *ExecutionFsStore) ListLogs(ctx context.Context, query executions.LogQuery) ([]executions.Log, error) {
	if ctx == nil {
		return nil, errors.New("execution log database requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	var anchor storedLog
	if query.AfterLogID != "" {
		var err error
		anchor, err = s.readStoredLog(ctx, query.AfterLogID)
		if err != nil {
			return nil, err
		}
	}
	index, _, err := s.readLogIndex(ctx)
	if err != nil {
		return nil, err
	}
	after := uint64(0)
	if query.AfterLogID != "" {
		if anchor.Sequence > index.Committed || !query.Matches(anchor.Log) {
			return nil, executions.ErrUnknownLog
		}
		if err := s.checkLogPosition(ctx, index, anchor); err != nil {
			return nil, err
		}
		after = anchor.Sequence
	}
	items := make([]executions.Log, 0)
	for sequence := after; sequence < index.Committed && len(items) < query.Limit; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sequence++
		position, err := s.readLogPosition(ctx, sequence)
		if err != nil {
			return nil, err
		}
		record, err := s.readStoredLog(ctx, position.LogID)
		if err != nil {
			return nil, err
		}
		if record.Sequence != sequence {
			return nil, errors.New("execution log position disagrees with its saved record")
		}
		if query.Matches(record.Log) {
			items = append(items, record.Log)
		}
	}
	return items, nil
}
