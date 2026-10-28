package executions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

type storedLog struct {
	Sequence uint64         `json:"sequence"`
	Log      executions.Log `json:"log"`
}

func (s *ExecutionFsStore) AppendLog(ctx context.Context, log executions.Log) error {
	if _, err := s.logPath(ctx, log.ID); err != nil {
		return err
	}
	if err := log.Validate(); err != nil {
		return err
	}
	if _, err := json.Marshal(log); err != nil {
		return err
	}
	if err := prepareLogDirectory(s.logDirectory()); err != nil {
		return err
	}
	coordination, err := jsonfile.OpenContext(ctx, filepath.Join(s.logDirectory(), "coordination.json"))
	if err != nil {
		return err
	}
	return coordination.View(ctx, func(data []byte) error {
		if !bytes.Equal(bytes.TrimSpace(data), []byte("[]")) {
			return errors.New("execution log coordination file must be empty")
		}
		return s.appendLog(ctx, log)
	})
}

func (s *ExecutionFsStore) appendLog(ctx context.Context, log executions.Log) error {
	index, exists, err := s.readLogIndex(ctx)
	if err != nil {
		return err
	}
	if exists {
		// The durable head read confirms an earlier uncertain publication before recovery.
		if index.Pending != nil {
			index, err = s.finishLogAppend(ctx, index)
			if err != nil {
				return err
			}
		}
	}
	record, err := s.readStoredLog(ctx, log.ID)
	if err == nil {
		if err := s.checkLogPosition(ctx, index, record); err != nil {
			return err
		}
		if !record.Log.Equal(log) {
			return executions.ErrLogConflict
		}
		return s.publishStoredLog(ctx, record)
	}
	if !errors.Is(err, executions.ErrUnknownLog) {
		return err
	}
	if index.Committed == ^uint64(0) {
		return errors.New("execution log sequence is exhausted")
	}
	next := logIndex{Committed: index.Committed, Pending: &storedLog{Sequence: index.Committed + 1, Log: log}}
	var previous *logIndex
	if exists {
		previous = &index
	}
	if err := s.publishLogIndex(ctx, previous, next); err != nil {
		return err
	}
	_, err = s.finishLogAppend(ctx, next)
	return err
}

func (s *ExecutionFsStore) GetLog(ctx context.Context, logID string) (executions.Log, error) {
	if _, err := s.logPath(ctx, logID); err != nil {
		return executions.Log{}, err
	}
	record, err := s.readStoredLog(ctx, logID)
	if err != nil {
		return executions.Log{}, err
	}
	index, _, err := s.readLogIndex(ctx)
	if err == nil {
		err = s.checkLogPosition(ctx, index, record)
	}
	if err != nil {
		return executions.Log{}, err
	}
	return record.Log, nil
}

func (s *ExecutionFsStore) readStoredLog(ctx context.Context, logID string) (storedLog, error) {
	path, err := s.logPath(ctx, logID)
	if err != nil {
		return storedLog{}, err
	}
	data, err := jsonfile.Read(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return storedLog{}, executions.ErrUnknownLog
	}
	if err != nil {
		return storedLog{}, err
	}
	return decodeStoredLog(data, logID)
}

func (s *ExecutionFsStore) publishStoredLog(ctx context.Context, record storedLog) error {
	path, err := s.logPath(ctx, record.Log.ID)
	if err != nil {
		return err
	}
	_, readErr := s.readStoredLog(ctx, record.Log.ID)
	if readErr != nil && !errors.Is(readErr, executions.ErrUnknownLog) {
		return readErr
	}
	encoded, err := json.Marshal([]storedLog{record})
	if err != nil {
		return err
	}
	collection, err := jsonfile.OpenContext(ctx, path)
	if err != nil {
		return err
	}
	return collection.Update(ctx, func(data []byte) ([]byte, error) {
		if errors.Is(readErr, executions.ErrUnknownLog) && bytes.Equal(bytes.TrimSpace(data), []byte("[]")) {
			return encoded, nil
		}
		current, err := decodeStoredLog(data, record.Log.ID)
		if err != nil {
			return nil, err
		}
		if !sameStoredLog(current, record) {
			return nil, errors.New("execution log record disagrees with its append reservation")
		}
		return data, nil
	})
}

func (s *ExecutionFsStore) logDirectory() string { return filepath.Join(s.root, "execution_logs") }

func (s *ExecutionFsStore) logPath(ctx context.Context, logID string) (string, error) {
	if ctx == nil {
		return "", errors.New("execution log database requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !id.Valid(logID) {
		return "", errors.New("execution log ID must be a canonical UUID")
	}
	return filepath.Join(s.logDirectory(), logID+".json"), nil
}

func decodeStoredLog(data []byte, logID string) (storedLog, error) {
	var records []storedLog
	if err := decodeLogArray(data, &records); err != nil {
		return storedLog{}, err
	}
	if len(records) != 1 || records[0].Sequence == 0 {
		return storedLog{}, errors.New("execution log file must contain one positioned record")
	}
	record := records[0]
	if err := record.Log.Validate(); err != nil {
		return storedLog{}, err
	}
	if record.Log.ID != logID {
		return storedLog{}, errors.New("execution log file does not match its identity")
	}
	return record, nil
}

func sameStoredLog(left, right storedLog) bool {
	return left.Sequence == right.Sequence && left.Log.Equal(right.Log)
}

func decodeLogArray(data []byte, destination any) error {
	if !utf8.Valid(data) {
		return errors.New("execution log state must contain valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode execution log state: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("execution log state must contain exactly one JSON array")
	}
	return nil
}
