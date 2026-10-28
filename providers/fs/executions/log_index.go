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
	"strings"

	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

type logIndex struct {
	Committed uint64     `json:"committed"`
	Pending   *storedLog `json:"pending,omitempty"`
}

type logPosition struct {
	Sequence uint64 `json:"sequence"`
	LogID    string `json:"log_id"`
}

func (s *ExecutionFsStore) readLogIndex(ctx context.Context) (logIndex, bool, error) {
	data, err := jsonfile.ReadDurable(ctx, filepath.Join(s.logDirectory(), "index.json"))
	if errors.Is(err, os.ErrNotExist) {
		unindexed := s.checkUnindexedLogs(ctx)
		if unindexed == nil {
			return logIndex{}, false, nil
		}
		// A first append can publish its index between the missing read and inspection.
		data, err = jsonfile.ReadDurable(ctx, filepath.Join(s.logDirectory(), "index.json"))
		if errors.Is(err, os.ErrNotExist) {
			return logIndex{}, false, unindexed
		}
	}
	if err != nil {
		return logIndex{}, false, err
	}
	index, err := decodeLogIndex(data)
	if err == nil && index.Committed != 0 {
		position, positionErr := s.readLogPosition(ctx, index.Committed)
		if positionErr != nil {
			return logIndex{}, false, positionErr
		}
		record, recordErr := s.readStoredLog(ctx, position.LogID)
		if recordErr != nil {
			return logIndex{}, false, recordErr
		}
		if record.Sequence != index.Committed {
			return logIndex{}, false, errors.New("execution log head disagrees with its committed tail")
		}
	}
	return index, true, err
}

func (s *ExecutionFsStore) publishLogIndex(ctx context.Context, previous *logIndex, next logIndex) error {
	data, err := json.Marshal([]logIndex{next})
	if err != nil {
		return err
	}
	collection, err := jsonfile.OpenContext(ctx, filepath.Join(s.logDirectory(), "index.json"))
	if err != nil {
		return err
	}
	return collection.Update(ctx, func(current []byte) ([]byte, error) {
		if previous == nil && bytes.Equal(bytes.TrimSpace(current), []byte("[]")) {
			return data, nil
		}
		index, err := decodeLogIndex(current)
		if err != nil {
			return nil, err
		}
		if previous == nil || !sameLogIndex(index, *previous) {
			return nil, errors.New("execution log index changed outside its coordinator")
		}
		return data, nil
	})
}

func (s *ExecutionFsStore) finishLogAppend(ctx context.Context, index logIndex) (logIndex, error) {
	if err := s.publishStoredLog(ctx, *index.Pending); err != nil {
		return logIndex{}, err
	}
	if err := s.publishLogPosition(ctx, logPosition{Sequence: index.Pending.Sequence, LogID: index.Pending.Log.ID}); err != nil {
		return logIndex{}, err
	}
	next := logIndex{Committed: index.Pending.Sequence}
	if err := s.publishLogIndex(ctx, &index, next); err != nil {
		return logIndex{}, err
	}
	return next, nil
}

func (s *ExecutionFsStore) readLogPosition(ctx context.Context, sequence uint64) (logPosition, error) {
	data, err := jsonfile.Read(ctx, s.logPositionPath(sequence))
	if err != nil {
		return logPosition{}, fmt.Errorf("read execution log position %d: %w", sequence, err)
	}
	return decodeLogPosition(data, sequence)
}

func (s *ExecutionFsStore) publishLogPosition(ctx context.Context, position logPosition) error {
	path := s.logPositionPath(position.Sequence)
	if err := prepareLogDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	_, readErr := jsonfile.Read(ctx, path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	encoded, err := json.Marshal([]logPosition{position})
	if err != nil {
		return err
	}
	collection, err := jsonfile.OpenContext(ctx, path)
	if err != nil {
		return err
	}
	return collection.Update(ctx, func(data []byte) ([]byte, error) {
		if errors.Is(readErr, os.ErrNotExist) && bytes.Equal(bytes.TrimSpace(data), []byte("[]")) {
			return encoded, nil
		}
		current, err := decodeLogPosition(data, position.Sequence)
		if err != nil {
			return nil, err
		}
		if current != position {
			return nil, errors.New("execution log position belongs to another record")
		}
		return data, nil
	})
}

func (s *ExecutionFsStore) checkLogPosition(ctx context.Context, index logIndex, record storedLog) error {
	if record.Sequence > index.Committed {
		return errors.New("execution log record has not been committed")
	}
	position, err := s.readLogPosition(ctx, record.Sequence)
	if err != nil {
		return err
	}
	if position.LogID != record.Log.ID {
		return errors.New("execution log record disagrees with its committed position")
	}
	return nil
}

func (s *ExecutionFsStore) logPositionPath(sequence uint64) string {
	return filepath.Join(s.logDirectory(), "positions", fmt.Sprintf("%020d.json", sequence))
}

func decodeLogIndex(data []byte) (logIndex, error) {
	var records []struct {
		Committed *uint64    `json:"committed"`
		Pending   *storedLog `json:"pending,omitempty"`
	}
	if err := decodeLogArray(data, &records); err != nil {
		return logIndex{}, err
	}
	if len(records) != 1 || records[0].Committed == nil {
		return logIndex{}, errors.New("execution log index must contain one control record")
	}
	index := logIndex{Committed: *records[0].Committed, Pending: records[0].Pending}
	if index.Committed == 0 && index.Pending == nil {
		return logIndex{}, errors.New("execution log index must contain committed or pending work")
	}
	if index.Pending != nil {
		if index.Committed == ^uint64(0) || index.Pending.Sequence != index.Committed+1 {
			return logIndex{}, errors.New("execution log pending position must follow its committed head")
		}
		if err := index.Pending.Log.Validate(); err != nil {
			return logIndex{}, err
		}
	}
	return index, nil
}

func decodeLogPosition(data []byte, sequence uint64) (logPosition, error) {
	var records []logPosition
	if err := decodeLogArray(data, &records); err != nil {
		return logPosition{}, err
	}
	if len(records) != 1 || sequence == 0 || records[0].Sequence != sequence || !id.Valid(records[0].LogID) {
		return logPosition{}, errors.New("execution log position must contain its matching sequence and log ID")
	}
	return records[0], nil
}

func sameLogIndex(left, right logIndex) bool {
	if left.Committed != right.Committed || (left.Pending == nil) != (right.Pending == nil) {
		return false
	}
	return left.Pending == nil || sameStoredLog(*left.Pending, *right.Pending)
}

func prepareLogDirectory(directory string) error {
	if info, err := os.Lstat(directory); err == nil {
		if !info.IsDir() {
			return errors.New("execution log directory must not be a symlink or file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return jsonfile.PrepareDirectory(directory)
}

func (s *ExecutionFsStore) checkUnindexedLogs(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := s.logDirectory()
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("execution log directory must not be a symlink or file")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := file.ReadDir(8)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			if name != "coordination.json.lock" && name != "index.json.lock" &&
				!(strings.HasPrefix(name, ".index.json-") && strings.HasSuffix(name, ".tmp")) {
				return errors.New("execution log records exist without a committed index")
			}
		}
		if errors.Is(err, io.EOF) {
			return ctx.Err()
		}
	}
}
