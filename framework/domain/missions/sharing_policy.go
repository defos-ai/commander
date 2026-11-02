package missions

import (
	"errors"
	"fmt"
	"slices"
)

type SharingKind string

const (
	ShareResults   SharingKind = "results"
	ShareArtifacts SharingKind = "artifacts"
	ShareDecisions SharingKind = "decisions"
)

type SharingPolicy []SharingRoute

type SharingRoute struct {
	FromActions []string      `json:"from_actions"`
	ToActions   []string      `json:"to_actions"`
	Include     []SharingKind `json:"include"`
}

func (p SharingPolicy) Validate() error {
	for i, sharing := range p {
		if err := sharing.validate(); err != nil {
			return fmt.Errorf("sharing[%d]: %w", i, err)
		}
	}
	return nil
}

func (s SharingRoute) validate() error {
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
	seen := make(map[SharingKind]bool, len(s.Include))
	for _, kind := range s.Include {
		switch kind {
		case ShareResults, ShareArtifacts, ShareDecisions:
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

func (p SharingPolicy) Clone() SharingPolicy {
	p = slices.Clone(p)
	for i := range p {
		p[i].FromActions = slices.Clone(p[i].FromActions)
		p[i].ToActions = slices.Clone(p[i].ToActions)
		p[i].Include = slices.Clone(p[i].Include)
	}
	return p
}
