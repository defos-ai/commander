package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"commander/framework/id"
)

var ErrUnknownArtifact = errors.New("unknown artifact")

// Artifact describes exactly one source: bytes or a local file. Empty, non-nil
// Data is valid. Sources are local implementation details and never serialized.
type Artifact struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Data      []byte `json:"-"`
	Path      string `json:"-"`
}

func (a Artifact) Validate() error {
	if !utf8.ValidString(a.Name) || !utf8.ValidString(a.MediaType) {
		return fmt.Errorf("artifact name and media type must contain valid UTF-8")
	}
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("artifact name must not be blank")
	}
	if (a.Data != nil) == (a.Path != "") {
		return fmt.Errorf("artifact requires exactly one source: data or path")
	}
	return nil
}

// Ref is portable metadata. It deliberately contains no local storage path.
type Ref struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	MediaType  string    `json:"media_type"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
	AgentID    string    `json:"agent_id"`
	ActionName string    `json:"action_name"`
	CommandID  string    `json:"command_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (r Ref) Validate() error {
	if !id.Valid(r.ID) {
		return fmt.Errorf("artifact ID must be a canonical UUID")
	}
	if strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.MediaType) == "" {
		return fmt.Errorf("artifact name and media type must not be blank")
	}
	if !utf8.ValidString(r.Name) || !utf8.ValidString(r.MediaType) {
		return fmt.Errorf("artifact name and media type must contain valid UTF-8")
	}
	if r.Size < 0 {
		return fmt.Errorf("artifact size must not be negative")
	}
	digest, err := hex.DecodeString(r.SHA256)
	if err != nil || len(digest) != sha256.Size || r.SHA256 != strings.ToLower(r.SHA256) {
		return fmt.Errorf("artifact SHA256 must contain 64 lowercase hexadecimal characters")
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("artifact timestamp must be set")
	}
	return (Owner{AgentID: r.AgentID, ActionName: r.ActionName, CommandID: r.CommandID}).Validate()
}

type Owner struct {
	AgentID    string `json:"agent_id"`
	ActionName string `json:"action_name"`
	CommandID  string `json:"command_id,omitempty"`
}

func (o Owner) Validate() error {
	if !id.ValidAgent(o.AgentID) {
		return fmt.Errorf("invalid artifact agent ID")
	}
	if o.CommandID != "" && !id.Valid(o.CommandID) {
		return fmt.Errorf("artifact command ID must be a UUID")
	}
	if strings.TrimSpace(o.ActionName) == "" {
		return fmt.Errorf("artifact action name must not be blank")
	}
	if !utf8.ValidString(o.ActionName) {
		return fmt.Errorf("artifact action name must contain valid UTF-8")
	}
	return nil
}
