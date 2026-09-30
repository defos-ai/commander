package artifact

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"commander/framework/id"
)

var (
	ErrUnknownArtifact  = errors.New("unknown artifact")
	ErrArtifactConflict = errors.New("artifact ID already has different metadata")
)

type Artifact struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	AgentID    string         `json:"agent_id"`
	ActionName string         `json:"action_name"`
	CommandID  string         `json:"command_id,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	Files      []ArtifactFile `json:"files"`
}

func (a Artifact) Validate() error {
	if !id.Valid(a.ID) {
		return errors.New("artifact ID must be a canonical UUID")
	}
	if strings.TrimSpace(a.Name) == "" || !utf8.ValidString(a.Name) {
		return errors.New("artifact name must contain nonblank UTF-8 text")
	}
	if a.CreatedAt.IsZero() {
		return errors.New("artifact timestamp must be set")
	}
	if err := (Owner{AgentID: a.AgentID, ActionName: a.ActionName, CommandID: a.CommandID}).Validate(); err != nil {
		return err
	}
	seen := make(map[string]bool, len(a.Files))
	for _, file := range a.Files {
		if err := file.Validate(); err != nil {
			return fmt.Errorf("artifact file: %w", err)
		}
		if seen[file.ID] {
			return errors.New("artifact contains duplicate file IDs")
		}
		seen[file.ID] = true
	}
	return nil
}

func (a Artifact) Clone() Artifact {
	a.Files = slices.Clone(a.Files)
	return a
}

func (a Artifact) Equal(b Artifact) bool {
	return a.ID == b.ID && a.Name == b.Name && a.AgentID == b.AgentID && a.ActionName == b.ActionName && a.CommandID == b.CommandID && a.CreatedAt.Equal(b.CreatedAt) && slices.Equal(a.Files, b.Files)
}

func CloneAll(artifacts []Artifact) []Artifact {
	result := slices.Clone(artifacts)
	for i := range result {
		result[i] = result[i].Clone()
	}
	return result
}

type Owner struct {
	AgentID    string `json:"agent_id"`
	ActionName string `json:"action_name"`
	CommandID  string `json:"command_id,omitempty"`
}

func (o Owner) Validate() error {
	if !id.ValidAgent(o.AgentID) {
		return errors.New("invalid artifact agent ID")
	}
	if o.CommandID != "" && !id.Valid(o.CommandID) {
		return errors.New("artifact command ID must be a UUID")
	}
	if strings.TrimSpace(o.ActionName) == "" || !utf8.ValidString(o.ActionName) {
		return errors.New("artifact action name must contain nonblank UTF-8 text")
	}
	return nil
}
