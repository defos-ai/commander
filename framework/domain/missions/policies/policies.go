package policies

import "fmt"

type MissionPolicies struct {
	Placement    *PlacementPolicy    `json:"placement,omitempty"`
	Continuation *ContinuationPolicy `json:"continuation,omitempty"`
	Sharing      SharingPolicy       `json:"sharing,omitempty"`
}

func (p MissionPolicies) Validate() error {
	if p.Placement != nil {
		if err := p.Placement.Validate(); err != nil {
			return fmt.Errorf("placement: %w", err)
		}
	}
	if p.Continuation != nil {
		if err := p.Continuation.Validate(); err != nil {
			return fmt.Errorf("continuation: %w", err)
		}
	}
	return p.Sharing.Validate()
}

func (p MissionPolicies) Clone() MissionPolicies {
	if p.Placement != nil {
		placement := p.Placement.Clone()
		p.Placement = &placement
	}
	if p.Continuation != nil {
		continuation := p.Continuation.Clone()
		p.Continuation = &continuation
	}
	p.Sharing = p.Sharing.Clone()
	return p
}
