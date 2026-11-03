package policies

import "slices"

type PlacementPolicy struct {
	// SameAgent binds one action group to the first agent dispatched for it in an operation.
	SameAgent []string `json:"same_agent,omitempty"`
}

func (p PlacementPolicy) Validate() error {
	return validateActionNames("same_agent", p.SameAgent)
}

func (p PlacementPolicy) Clone() PlacementPolicy {
	p.SameAgent = slices.Clone(p.SameAgent)
	return p
}
