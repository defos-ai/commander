package docker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/framework/id"
)

// Persist intent before creating anything on the daemon. The random owner label
// lets a retry distinguish our container after an uncertain Create response.
// No credentials or environment values belong in this record.
type record struct {
	RequestID string `json:"request_id"`
	Package   string `json:"package"`
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	Image     string `json:"image"`
	Network   string `json:"network"`
	Owner     string `json:"owner"`
}

func (r record) name() string { return "commander-" + r.AgentID }
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (r record) labels() map[string]string {
	return map[string]string{
		"ai.defos.commander.spawn-owner":   r.Owner,
		"ai.defos.commander.spawn-request": digest(r.RequestID),
		"ai.defos.commander.agent-id":      r.AgentID,
		"ai.defos.commander.package":       r.Package,
	}
}
func (m *Manager) recordPath(requestID string) string {
	return filepath.Join(m.config.Directory, "records", digest(requestID)+".json")
}

func (m *Manager) readRecord(requestID, reference string) (record, bool, error) {
	file, err := os.Open(m.recordPath(requestID))
	if errors.Is(err, os.ErrNotExist) {
		return record{}, false, nil
	}
	if err != nil {
		return record{}, true, fmt.Errorf("read Docker launch record: %w", err)
	}
	defer file.Close()
	var saved record
	decoder := json.NewDecoder(io.LimitReader(file, 16*1024))
	decoder.DisallowUnknownFields()
	invalid := errors.New("Docker launch record is invalid or incomplete; refusing another container")
	if decoder.Decode(&saved) != nil || decoder.Decode(new(any)) != io.EOF {
		return record{}, true, invalid
	}
	kind, valid := id.RuntimeType(saved.AgentID, id.Agent)
	ref, parseErr := packages.ParseReference(saved.Package)
	manifest := packages.Manifest{SchemaVersion: 1, Type: saved.AgentType, Version: ref.Version, Docker: &packages.Docker{Image: saved.Image}}
	if saved.RequestID != requestID || !valid || kind != strings.TrimSuffix(saved.AgentType, "-agent") || !id.Valid(saved.Owner) || parseErr != nil || manifest.Validate() != nil || !explicitNetwork(saved.Network) {
		return record{}, true, invalid
	}
	if saved.Package != reference {
		return record{}, true, errors.New("launch request already belongs to a different package")
	}
	if saved.Network != m.config.Network {
		return record{}, true, errors.New("recorded Docker network differs from spawner configuration")
	}
	return saved, true, nil
}

func (m *Manager) reserve(saved record) error {
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(m.recordPath(saved.RequestID), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("reserve Docker launch request (intents are never overwritten): %w", err)
	}
	_, writeErr := io.Copy(file, bytes.NewReader(data))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("persist Docker launch intent: %w", err)
	}
	return syncDirectory(filepath.Dir(m.recordPath(saved.RequestID)))
}
