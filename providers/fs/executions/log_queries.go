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
	if query.Order == executions.LogOrderDescending {
		before := index.Committed
		if after != 0 {
			before = after - 1
		}
		return s.descendingLogs(ctx, query, before)
	}
	items := make([]executions.Log, 0)
	for sequence := after; sequence < index.Committed && len(items) < query.Limit; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sequence++
		log, err := s.logAtSequence(ctx, sequence)
		if err != nil {
			return nil, err
		}
		if query.Matches(log) {
			items = append(items, log)
		}
	}
	return items, nil
}

func (s *ExecutionFsStore) descendingLogs(ctx context.Context, query executions.LogQuery, head uint64) ([]executions.Log, error) {
	items := make([]executions.Log, 0)
	for sequence := head; sequence > 0 && len(items) < query.Limit; sequence-- {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		log, err := s.logAtSequence(ctx, sequence)
		if err != nil {
			return nil, err
		}
		if query.Matches(log) {
			items = append(items, log)
		}
	}
	return items, nil
}

func (s *ExecutionFsStore) logAtSequence(ctx context.Context, sequence uint64) (executions.Log, error) {
	position, err := s.readLogPosition(ctx, sequence)
	if err != nil {
		return executions.Log{}, err
	}
	record, err := s.readStoredLog(ctx, position.LogID)
	if err != nil {
		return executions.Log{}, err
	}
	if record.Sequence != sequence {
		return executions.Log{}, errors.New("execution log position disagrees with its saved record")
	}
	return record.Log, nil
}
