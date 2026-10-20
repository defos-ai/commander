package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/id"
)

// An immutable intent fences retries even if Start succeeded just before a
// crash. PID reuse cannot prove ownership, so another host never adopts it.
type record struct {
	Runtime   string `json:"runtime"`
	RequestID string `json:"request_id"`
	Package   string `json:"package"`
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
}

func (m *Manager) existingRecord(ctx context.Context, requestID, reference string) error {
	data, found, err := action.LoadCheckpoint(ctx, "launch-intent")
	if err != nil {
		return fmt.Errorf("load launch intent: %w", err)
	}
	if !found {
		return nil
	}
	return validateExistingRecord(bytes.NewReader(data), requestID, reference)
}

func validateExistingRecord(reader io.Reader, requestID, reference string) error {
	var saved record
	decoder := json.NewDecoder(io.LimitReader(reader, 16*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&saved); err != nil {
		return errors.New("launch record is incomplete; launch outcome is uncertain, refusing to start another process")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("launch record is invalid; refusing to start another process")
	}
	kind, validID := id.RuntimeType(saved.AgentID, id.Agent)
	pinned, pinErr := packages.ParseReference(saved.Package)
	if saved.Runtime != "native" || saved.RequestID != requestID || !validID || kind != strings.TrimSuffix(saved.AgentType, "-agent") ||
		pinErr != nil || pinned.String() != saved.Package {
		return errors.New("launch record identity is invalid; refusing to start another process")
	}
	if saved.Package != reference {
		return errors.New("launch request already belongs to a different package")
	}
	return fmt.Errorf("launch outcome for %s is uncertain after host restart; inspect the previous instance before requesting a replacement", saved.AgentID)
}

func (m *Manager) reserve(ctx context.Context, saved record) error {
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if err := action.SaveCheckpoint(ctx, "launch-intent", data); err != nil {
		return fmt.Errorf("persist launch intent: %w", err)
	}
	return nil
}
