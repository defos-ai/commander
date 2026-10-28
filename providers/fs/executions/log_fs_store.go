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
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

type LogFsStore struct{ directory string }

var _ executions.LogStore = (*LogFsStore)(nil)

func NewLogFsStore(root string) (*LogFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("execution log database root must not be blank")
	}
	directory, err := filepath.Abs(filepath.Join(root, "execution_logs"))
	if err != nil {
		return nil, err
	}
	return &LogFsStore{directory: directory}, nil
}

func (s *LogFsStore) Append(ctx context.Context, log executions.Log) error {
	path, err := s.path(ctx, log.ID)
	if err != nil {
		return err
	}
	if err := log.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal([]executions.Log{log})
	if err != nil {
		return err
	}
	if info, err := os.Lstat(s.directory); err == nil {
		if !info.IsDir() {
			return errors.New("execution log directory must not be a symlink or file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := jsonfile.PrepareDirectory(s.directory); err != nil {
		return err
	}
	collection, err := jsonfile.OpenContext(ctx, path)
	if err != nil {
		return err
	}
	return collection.Update(ctx, func(data []byte) ([]byte, error) {
		current, err := decodeLog(data, log.ID)
		if errors.Is(err, executions.ErrUnknownLog) {
			return encoded, nil
		}
		if err != nil {
			return nil, err
		}
		if !current.Equal(log) {
			return nil, executions.ErrLogConflict
		}
		// Republish the original bytes to confirm durability after an uncertain write.
		return data, nil
	})
}

func (s *LogFsStore) Get(ctx context.Context, logID string) (executions.Log, error) {
	path, err := s.path(ctx, logID)
	if err != nil {
		return executions.Log{}, err
	}
	data, err := jsonfile.Read(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return executions.Log{}, executions.ErrUnknownLog
	}
	if err != nil {
		return executions.Log{}, err
	}
	return decodeLog(data, logID)
}

func (s *LogFsStore) path(ctx context.Context, logID string) (string, error) {
	if ctx == nil {
		return "", errors.New("execution log database requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !id.Valid(logID) {
		return "", errors.New("execution log ID must be a canonical UUID")
	}
	return filepath.Join(s.directory, logID+".json"), nil
}

func decodeLog(data []byte, logID string) (executions.Log, error) {
	if !utf8.Valid(data) {
		return executions.Log{}, errors.New("execution log file must contain valid UTF-8")
	}
	var records []executions.Log
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		return executions.Log{}, fmt.Errorf("decode execution log: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return executions.Log{}, errors.New("execution log file must contain exactly one JSON array")
	}
	if records == nil {
		return executions.Log{}, errors.New("execution log file must contain a JSON array")
	}
	if len(records) == 0 {
		return executions.Log{}, executions.ErrUnknownLog
	}
	if len(records) != 1 {
		return executions.Log{}, errors.New("execution log file must contain exactly one record")
	}
	log := records[0]
	if err := log.Validate(); err != nil {
		return executions.Log{}, err
	}
	if log.ID != logID {
		return executions.Log{}, errors.New("execution log file does not match its identity")
	}
	return log, nil
}
