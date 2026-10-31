// Package observations is the public, agent-independent assessment evidence contract.
package observations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type Identity struct {
	Namespace string `json:"namespace"`
	Value     string `json:"value"`
	Evidence  string `json:"evidence"`
}

type Endpoint struct {
	NetworkID string `json:"network_id"`
	Address   string `json:"address,omitempty"`
}

type Record struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	ObservedAt   *time.Time      `json:"observed_at,omitempty"`
	NetworkID    string          `json:"network_id,omitempty"`
	Address      string          `json:"address,omitempty"`
	Hostname     string          `json:"hostname,omitempty"`
	Identity     *Identity       `json:"identity,omitempty"`
	Port         *int            `json:"port,omitempty"`
	Transport    string          `json:"transport,omitempty"`
	Protocol     string          `json:"protocol,omitempty"`
	Product      string          `json:"product,omitempty"`
	Version      string          `json:"version,omitempty"`
	State        string          `json:"state,omitempty"`
	IssueID      string          `json:"issue_id,omitempty"`
	Title        string          `json:"title,omitempty"`
	Severity     string          `json:"severity,omitempty"`
	Description  string          `json:"description,omitempty"`
	Evidence     string          `json:"evidence,omitempty"`
	Source       *Endpoint       `json:"source,omitempty"`
	Destination  *Endpoint       `json:"destination,omitempty"`
	Relationship string          `json:"relationship,omitempty"`
	Directed     *bool           `json:"directed,omitempty"`
	Details      json.RawMessage `json:"details,omitempty"`
}

type Batch struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

func (b *Batch) Validate() error {
	if b == nil {
		return nil
	}
	if b.Version != 1 || b.Records == nil {
		return errors.New("observations require version 1 and an explicit records array")
	}
	ids := map[string]bool{}
	for _, record := range b.Records {
		if record.ID == "" || ids[record.ID] {
			return errors.New("observation IDs must be nonempty and distinct within a result")
		}
		ids[record.ID] = true
		switch record.Kind {
		case "host", "service", "finding":
			if strings.TrimSpace(record.Address) == "" {
				return errors.New("host/service/finding observation requires an address")
			}
		case "connection":
			if record.Source == nil || record.Destination == nil || record.Relationship == "" {
				return errors.New("connection requires endpoints and relationship meaning")
			}
		default:
			return fmt.Errorf("unknown observation kind %q", record.Kind)
		}
		if (record.Kind == "finding" || record.Kind == "connection") && strings.TrimSpace(record.Evidence) == "" {
			return errors.New("security issues and connections require supporting evidence")
		}
		if record.Kind == "finding" && strings.TrimSpace(record.Title) == "" {
			return errors.New("finding requires a title")
		}
		if record.Identity != nil && (record.Identity.Namespace == "" || record.Identity.Value == "" || record.Identity.Evidence == "") {
			return errors.New("shared host identity requires namespace, value and evidence")
		}
		if record.Port != nil && (*record.Port < 0 || *record.Port > 65535) {
			return errors.New("invalid observed port")
		}
		if len(record.Details) != 0 && !json.Valid(record.Details) {
			return errors.New("observation details must be JSON")
		}
	}
	return nil
}

func (b *Batch) Clone() *Batch {
	if b == nil {
		return nil
	}
	result := *b
	result.Records = slices.Clone(b.Records)
	for i := range result.Records {
		record := &result.Records[i]
		record.Details = bytes.Clone(record.Details)
		if record.ObservedAt != nil {
			value := *record.ObservedAt
			record.ObservedAt = &value
		}
		if record.Identity != nil {
			value := *record.Identity
			record.Identity = &value
		}
		if record.Source != nil {
			value := *record.Source
			record.Source = &value
		}
		if record.Destination != nil {
			value := *record.Destination
			record.Destination = &value
		}
		if record.Port != nil {
			value := *record.Port
			record.Port = &value
		}
		if record.Directed != nil {
			value := *record.Directed
			record.Directed = &value
		}
	}
	return &result
}
