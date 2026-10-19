package native

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

	"github.com/defos-ai/commander/framework/id"
)

// A create-only intent fences retries even if Start succeeded just before a
// crash. PID reuse cannot prove ownership, so another host never adopts it.
type record struct {
	RequestID string `json:"request_id"`
	Package   string `json:"package"`
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
}

func (m *Manager) recordPath(requestID string) string {
	digest := sha256.Sum256([]byte(requestID))
	return filepath.Join(m.config.Directory, "records", hex.EncodeToString(digest[:])+".json")
}

func (m *Manager) existingRecord(requestID, reference string) error {
	file, err := os.Open(m.recordPath(requestID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect launch record: %w", err)
	}
	defer file.Close()
	var saved record
	decoder := json.NewDecoder(io.LimitReader(file, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&saved); err != nil {
		return errors.New("launch record is incomplete; launch outcome is uncertain, refusing to start another process")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("launch record is invalid; refusing to start another process")
	}
	kind, validID := id.RuntimeType(saved.AgentID, id.Agent)
	if saved.RequestID != requestID || !validID || kind != strings.TrimSuffix(saved.AgentType, "-agent") {
		return errors.New("launch record identity is invalid; refusing to start another process")
	}
	if saved.Package != reference {
		return errors.New("launch request already belongs to a different package")
	}
	return fmt.Errorf("launch outcome for %s is uncertain after host restart; inspect the previous instance before requesting a replacement", saved.AgentID)
}

func (m *Manager) reserve(saved record) error {
	data, err := json.Marshal(saved)
	if err != nil {
		return err
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
