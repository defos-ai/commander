package missions

import (
	"errors"
	"fmt"
	"slices"
)

type ContextKind string

const (
	ContextResults   ContextKind = "results"
	ContextArtifacts ContextKind = "artifacts"
	ContextDecisions ContextKind = "decisions"
)

type ContextPolicy struct {
	Sharing []ContextSharing `json:"sharing,omitempty"`
}

type ContextSharing struct {
	FromActions []string      `json:"from_actions"`
	ToActions   []string      `json:"to_actions"`
	Include     []ContextKind `json:"include"`
}

func (p ContextPolicy) Validate() error {
	for i, sharing := range p.Sharing {
		if err := sharing.validate(); err != nil {
			return fmt.Errorf("context.sharing[%d]: %w", i, err)
		}
	}
	return nil
}

func (s ContextSharing) validate() error {
	for _, list := range []struct {
		name    string
		actions []string
	}{{"from_actions", s.FromActions}, {"to_actions", s.ToActions}} {
		if len(list.actions) == 0 {
			return fmt.Errorf("%s must not be empty", list.name)
		}
		if err := validateActionNames(list.name, list.actions); err != nil {
			return err
		}
	}
	if len(s.Include) == 0 {
		return errors.New("include must not be empty")
	}
	seen := make(map[ContextKind]bool, len(s.Include))
	for _, kind := range s.Include {
		switch kind {
		case ContextResults, ContextArtifacts, ContextDecisions:
		default:
			return fmt.Errorf("unsupported include kind %q", kind)
		}
		if seen[kind] {
			return fmt.Errorf("include contains duplicate kind %q", kind)
		}
		seen[kind] = true
	}
	return nil
}

func (p ContextPolicy) Clone() ContextPolicy {
	p.Sharing = slices.Clone(p.Sharing)
	for i := range p.Sharing {
		p.Sharing[i].FromActions = slices.Clone(p.Sharing[i].FromActions)
		p.Sharing[i].ToActions = slices.Clone(p.Sharing[i].ToActions)
		p.Sharing[i].Include = slices.Clone(p.Sharing[i].Include)
	}
	return p
}
