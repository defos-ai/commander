package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	controllermodel "github.com/defos-ai/commander/framework/domain/controllers"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

// ControllerFsStore stores one single-record JSON collection per runtime in
// root/controllers/<controllerID>.json. The jsonfile lock is per controller,
// allowing unrelated registrations to update independently across processes.
type ControllerFsStore struct{ directory string }

var _ controllermodel.ControllerStore = (*ControllerFsStore)(nil)

func NewControllerFsStore(root string) (*ControllerFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	directory, err := filepath.Abs(filepath.Join(root, "controllers"))
	if err != nil {
		return nil, err
	}
	if err := jsonfile.PrepareDirectory(directory); err != nil {
		return nil, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	s := &ControllerFsStore{directory: directory}
	if _, err := s.List(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ControllerFsStore) Upsert(ctx context.Context, record controllermodel.Controller) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if err := s.checkDirectory(); err != nil {
		return err
	}
	record.Stale = false
	file, err := jsonfile.OpenContext(ctx, s.path(record.ID))
	if err != nil {
		return err
	}
	return file.Update(ctx, func(data []byte) ([]byte, error) {
		stored, err := decode(data, record.ID)
		if err != nil && !errors.Is(err, controllermodel.ErrUnknownController) {
			return nil, err
		}
		if err == nil && !record.LastHeartbeat.After(stored.LastHeartbeat) {
			return nil, controllermodel.ErrStaleObservation
		}
		return json.MarshalIndent([]controllermodel.Controller{record}, "", "  ")
	})
}

func (s *ControllerFsStore) Get(ctx context.Context, controllerID string) (controllermodel.Controller, error) {
	if err := ctx.Err(); err != nil {
		return controllermodel.Controller{}, err
	}
	if !id.ValidRuntime(controllerID, id.Controller) {
		return controllermodel.Controller{}, controllermodel.ErrUnknownController
	}
	if err := s.checkDirectory(); err != nil {
		return controllermodel.Controller{}, err
	}
	data, err := jsonfile.Read(ctx, s.path(controllerID))
	if errors.Is(err, os.ErrNotExist) {
		return controllermodel.Controller{}, controllermodel.ErrUnknownController
	}
	if err != nil {
		return controllermodel.Controller{}, err
	}
	return decode(data, controllerID)
}

func (s *ControllerFsStore) List(ctx context.Context) ([]controllermodel.Controller, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkDirectory(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return nil, err
	}
	records := []controllermodel.Controller{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		controllerID := strings.TrimSuffix(entry.Name(), ".json")
		if !id.ValidRuntime(controllerID, id.Controller) {
			return nil, fmt.Errorf("invalid controller record filename %q", entry.Name())
		}
		record, err := s.Get(ctx, controllerID)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records, nil
}

func (s *ControllerFsStore) path(controllerID string) string {
	return filepath.Join(s.directory, controllerID+".json")
}

func (s *ControllerFsStore) checkDirectory() error {
	info, err := os.Lstat(s.directory)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("controller inventory must be a directory, not a symlink")
	}
	return nil
}

func decode(data []byte, controllerID string) (controllermodel.Controller, error) {
	var records []controllermodel.Controller
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		return controllermodel.Controller{}, fmt.Errorf("decode controller registration: %w", err)
	}
	if len(records) == 0 {
		return controllermodel.Controller{}, controllermodel.ErrUnknownController
	}
	if len(records) != 1 || records[0].ID != controllerID {
		return controllermodel.Controller{}, errors.New("controller record must contain only the controller identified by its path")
	}
	if err := records[0].Validate(); err != nil {
		return controllermodel.Controller{}, err
	}
	return records[0], nil
}
