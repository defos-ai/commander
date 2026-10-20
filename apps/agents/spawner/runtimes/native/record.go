package native

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
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

func (m *Manager) recordPath(requestID string) string {
	digest := sha256.Sum256([]byte(requestID))
	return filepath.Join(m.config.Directory, "records", hex.EncodeToString(digest[:])+".json")
}

func (m *Manager) existingRecord(ctx context.Context, requestID, reference string) error {
	if runtimes.UsesExecutionCheckpoints(ctx) {
		// An execution always uses controller storage. A failed lookup must not
		// fall back to the standalone startup journal.
		data, found, err := action.LoadCheckpoint(ctx, "launch-intent")
		if err != nil {
			return fmt.Errorf("load launch intent: %w", err)
		}
		if !found {
			return nil
		}
		return validateExistingRecord(bytes.NewReader(data), requestID, reference)
	}
	file, err := os.Open(m.recordPath(requestID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect launch record: %w", err)
	}
	defer file.Close()
	return validateExistingRecord(file, requestID, reference)
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
	if runtimes.UsesExecutionCheckpoints(ctx) {
		if err := action.SaveCheckpoint(ctx, "launch-intent", data); err != nil {
			return fmt.Errorf("persist launch intent: %w", err)
		}
		return nil
	}
	file, err := os.OpenFile(m.recordPath(saved.RequestID), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("reserve launch request (existing intents are never overwritten): %w", err)
	}
	_, writeErr := io.Copy(file, bytes.NewReader(data))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		// Keep partial intent too: an uncertain durable write must not become
		// permission to launch another instance on retry.
		return fmt.Errorf("persist launch intent: %w", err)
	}
	return syncDirectory(filepath.Dir(m.recordPath(saved.RequestID)))
}
