package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/manifest"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/id"
)

// Persist intent before creating anything on the daemon. The random owner label
// lets a retry distinguish our container after an uncertain Create response.
// No credentials or environment values belong in this record.
type record struct {
	Runtime   string `json:"runtime"`
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

func (m *Manager) readRecord(ctx context.Context, requestID, reference string) (record, bool, error) {
	data, found, err := action.LoadCheckpoint(ctx, "launch-intent")
	if err != nil || !found {
		return record{}, found, err
	}
	return m.decodeRecord(bytes.NewReader(data), requestID, reference)
}

func (m *Manager) decodeRecord(source io.Reader, requestID, reference string) (record, bool, error) {
	var saved record
	decoder := json.NewDecoder(io.LimitReader(source, 16*1024))
	decoder.DisallowUnknownFields()
	invalid := errors.New("Docker launch record is invalid or incomplete; refusing another container")
	if decoder.Decode(&saved) != nil || decoder.Decode(new(any)) != io.EOF {
		return record{}, true, invalid
	}
	kind, valid := id.RuntimeType(saved.AgentID, id.Agent)
	ref, parseErr := packages.ParseReference(saved.Package)
	definition := manifest.Manifest{SchemaVersion: 1, Type: saved.AgentType, Version: ref.Version, Docker: &manifest.Docker{Image: saved.Image}}
	if saved.Runtime != "docker" || saved.RequestID != requestID || !valid || kind != strings.TrimSuffix(saved.AgentType, "-agent") || !id.Valid(saved.Owner) || parseErr != nil || definition.Validate() != nil || !explicitNetwork(saved.Network) {
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

func (m *Manager) reserve(ctx context.Context, saved record) error {
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if err := action.SaveCheckpoint(ctx, "launch-intent", data); err != nil {
		return err
	}
	m.ownedIntents[saved.RequestID] = saved.Owner
	return nil
}
