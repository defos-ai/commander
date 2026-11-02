package policies

type MissionPolicies struct {
	Sharing SharingPolicy `json:"sharing,omitempty"`
}

func (p MissionPolicies) Validate() error {
	return p.Sharing.Validate()
}

func (p MissionPolicies) Clone() MissionPolicies {
	p.Sharing = p.Sharing.Clone()
	return p
}
