package policies

import (
	"fmt"
	"slices"
)

type MissionPolicies struct {
	Continuation *ContinuationPolicy `json:"continuation,omitempty"`
	Completion   *CompletionPolicy   `json:"completion,omitempty"`
	Session      []SessionPolicy     `json:"session,omitempty"`
	Sharing      SharingPolicy       `json:"sharing,omitempty"`
}

func (p MissionPolicies) Validate() error {
	if p.Continuation != nil {
		if err := p.Continuation.Validate(); err != nil {
			return fmt.Errorf("continuation: %w", err)
		}
	}
	if p.Completion != nil {
		if err := p.Completion.Validate(); err != nil {
			return err
		}
	}
	if err := validateSessionPolicies(p.Session); err != nil {
		return err
	}
	return p.Sharing.Validate()
}

func (p MissionPolicies) Clone() MissionPolicies {
	if p.Continuation != nil {
		continuation := p.Continuation.Clone()
		p.Continuation = &continuation
	}
	if p.Completion != nil {
		completion := *p.Completion
		p.Completion = &completion
	}
	p.Session = slices.Clone(p.Session)
	for i := range p.Session {
		p.Session[i] = p.Session[i].Clone()
	}
	p.Sharing = p.Sharing.Clone()
	return p
}
