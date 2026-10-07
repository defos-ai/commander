package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

// AgentFsStore keeps one JSON observation per agent in
// root/agents/<agentID>.json. One persistent lock serializes observations and
// conditional lease expiry across processes.
type AgentFsStore struct{ root string }

// An expired record remains only while its source observation can still arrive
// within the controller's lifecycle freshness window.
type inventoryRecord struct {
	agentmodel.Agent
	Expired bool `json:"expired,omitempty"`
}

var _ agentmodel.AgentStore = (*AgentFsStore)(nil)

func NewAgentFsStore(root string) (*AgentFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(absolute, "agents", ".locks"), 0700); err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	s := &AgentFsStore{root: absolute}
	if _, err := s.List(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *AgentFsStore) directory() string {
	return filepath.Join(s.root, "agents")
}

func (s *AgentFsStore) locked(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock, err := jsonfile.NewContext(ctx, filepath.Join(s.directory(), ".locks", "inventory.json"))
	if err != nil {
		return err
	}
	completed := false
	err = lock.View(ctx, func([]byte) error {
		info, err := os.Lstat(s.directory())
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("agent inventory must be a directory, not a symlink")
		}
		err = fn()
		completed = err == nil
		return err
	})
	// A cancellation after a committed rename must not turn a successful
	// observation or expiry into a reported failure.
	if completed {
		return nil
	}
	return err
}

func (s *AgentFsStore) Upsert(ctx context.Context, record agentmodel.Agent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateInventoryRecord(record); err != nil {
		return err
	}
	snapshot := record.Clone()
	return s.locked(ctx, func() error {
		stored, err := s.read(ctx, snapshot.ID)
		if err != nil && !errors.Is(err, agentmodel.ErrUnknownAgent) {
			return err
		}
		if err == nil && !stored.ObservedAt.IsZero() && !snapshot.ObservedAt.After(stored.ObservedAt) {
			return agentmodel.ErrStaleObservation
		}
		_, err = s.write(ctx, inventoryRecord{Agent: snapshot})
		return err
	})
}

func (s *AgentFsStore) Get(ctx context.Context, agentID string) (agentmodel.Agent, error) {
	if err := ctx.Err(); err != nil {
		return agentmodel.Agent{}, err
	}
	if !id.ValidAgent(agentID) {
		return agentmodel.Agent{}, agentmodel.ErrUnknownAgent
	}
	var result agentmodel.Agent
	err := s.locked(ctx, func() error {
		stored, err := s.read(ctx, agentID)
		if err != nil {
			return err
		}
		if stored.Expired {
			return agentmodel.ErrUnknownAgent
		}
		result = stored.Agent
		return nil
	})
	return result, err
}

func (s *AgentFsStore) List(ctx context.Context) ([]agentmodel.Agent, error) {
	result := []agentmodel.Agent{}
	err := s.locked(ctx, func() error {
		records, err := s.list(ctx)
		if err != nil {
			return err
		}
		for _, record := range records {
			if !record.Expired {
				result = append(result, record.Agent)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *AgentFsStore) list(ctx context.Context) ([]inventoryRecord, error) {
	records := []inventoryRecord{}
	entries, err := os.ReadDir(s.directory())
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		agentID := strings.TrimSuffix(entry.Name(), ".json")
		if !id.ValidAgent(agentID) {
			return nil, fmt.Errorf("invalid inventory record filename %q", entry.Name())
		}
		record, err := s.read(ctx, agentID)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records, nil
}

// DeleteExpired compares each current heartbeat while holding the same lock as
// Upsert. Recent observation marks survive as hidden tombstones until cutoff
// catches up; returned records became absent, even if a later write fails.
func (s *AgentFsStore) DeleteExpired(ctx context.Context, cutoff time.Time) ([]agentmodel.Agent, error) {
	deleted := []agentmodel.Agent{}
	err := s.locked(ctx, func() error {
		records, err := s.list(ctx)
		if err != nil {
			return err
		}
		removed := false
		for _, record := range records {
			if record.Expired && record.ObservedAt.After(cutoff) ||
				!record.Expired && !record.LastHeartbeat.IsZero() && record.LastHeartbeat.After(cutoff) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if !record.Expired && record.ObservedAt.After(cutoff) {
				record.Expired = true
				committed, err := s.write(ctx, record)
				if committed {
					deleted = append(deleted, record.Agent)
				}
				if err != nil {
					return err
				}
				continue
			}
			if err := os.Remove(filepath.Join(s.directory(), record.ID+".json")); err != nil {
				return err
			}
			removed = true
			if !record.Expired {
				deleted = append(deleted, record.Agent)
			}
		}
		if removed {
			return syncInventoryDirectory(s.directory())
		}
		return nil
	})
	return deleted, err
}

func (s *AgentFsStore) read(ctx context.Context, agentID string) (inventoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return inventoryRecord{}, err
	}
	return readInventoryRecord(ctx, filepath.Join(s.directory(), agentID+".json"), agentID)
}

func readInventoryRecord(ctx context.Context, path, agentID string) (inventoryRecord, error) {
	if err := regularInventoryFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return inventoryRecord{}, agentmodel.ErrUnknownAgent
		}
		return inventoryRecord{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return inventoryRecord{}, err
	}
	var stored inventoryRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return inventoryRecord{}, fmt.Errorf("decode inventory record: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return inventoryRecord{}, errors.New("inventory file must contain exactly one record")
	}
	if err := validateInventoryRecord(stored.Agent); err != nil {
		return inventoryRecord{}, err
	}
	if stored.ID != agentID {
		return inventoryRecord{}, errors.New("inventory record identity does not match its path")
	}
	if err := ctx.Err(); err != nil {
		return inventoryRecord{}, err
	}
	return stored, nil
}

// write reports whether the rename committed, even if directory sync fails.
// Callers already read the existing observation under the inventory lock, so a
// replacement cannot conceal malformed records.
func (s *AgentFsStore) write(ctx context.Context, record inventoryRecord) (bool, error) {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return false, err
	}
	directory := s.directory()
	if err := os.MkdirAll(directory, 0700); err != nil {
		return false, err
	}
	file, err := os.CreateTemp(directory, "."+record.ID+"-*.tmp")
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return false, err
	}
	if err := file.Sync(); err != nil {
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	path := filepath.Join(directory, record.ID+".json")
	if err := regularInventoryFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return false, err
	}
	return true, syncInventoryDirectory(directory)
}

func regularInventoryFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("inventory record must be a regular file, not a symlink or directory")
	}
	return nil
}

func syncInventoryDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func validateInventoryRecord(record agentmodel.Agent) error {
	if !id.ValidAgent(record.ID) {
		return errors.New("invalid inventory agent ID")
	}
	if record.LastSeen.IsZero() {
		return errors.New("inventory observation must have a receipt timestamp")
	}
	return record.Server.Validate()
}
