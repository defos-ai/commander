package policies

import "fmt"

type MissionPolicies struct {
	Placement *PlacementPolicy `json:"placement,omitempty"`
	Sharing   SharingPolicy    `json:"sharing,omitempty"`
}

func (p MissionPolicies) Validate() error {
	if p.Placement != nil {
		if err := p.Placement.Validate(); err != nil {
			return fmt.Errorf("placement: %w", err)
		}
	}
	return p.Sharing.Validate()
}

func (p MissionPolicies) Clone() MissionPolicies {
	if p.Placement != nil {
		placement := p.Placement.Clone()
		p.Placement = &placement
	}
	p.Sharing = p.Sharing.Clone()
	return p
}
