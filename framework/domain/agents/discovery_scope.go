package agents

import "slices"

// DiscoveryScope constrains executable locations. A nil Paths uses normal search;
// an explicitly empty Paths searches nowhere. Services reserves named resources.
type DiscoveryScope struct {
	Paths    []string `json:"paths"`
	Services []string `json:"services"`
}

// Clone returns an independent scope, preserving nil and empty search paths.
// A nil scope remains nil.
func (s *DiscoveryScope) Clone() *DiscoveryScope {
	if s == nil {
		return nil
	}
	return &DiscoveryScope{Paths: slices.Clone(s.Paths), Services: slices.Clone(s.Services)}
}
