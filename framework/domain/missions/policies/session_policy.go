package policies

import (
	"errors"
	"fmt"
	"slices"
)

type SessionPolicy struct {
	CreateAction string   `json:"create_action"`
	UseActions   []string `json:"use_actions"`
}

func (p SessionPolicy) Validate() error {
	if err := validateActionNames("create_action", []string{p.CreateAction}); err != nil {
		return err
	}
	if len(p.UseActions) == 0 {
		return errors.New("use_actions must not be empty")
	}
	if err := validateActionNames("use_actions", p.UseActions); err != nil {
		return err
	}
	if slices.Contains(p.UseActions, p.CreateAction) {
		return errors.New("create_action must not also appear in use_actions")
	}
	return nil
}

func (p SessionPolicy) Clone() SessionPolicy {
	p.UseActions = slices.Clone(p.UseActions)
	return p
}

func validateSessionPolicies(policies []SessionPolicy) error {
	seen := make(map[string]int)
	for i, policy := range policies {
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("session[%d]: %w", i, err)
		}
		for _, name := range append([]string{policy.CreateAction}, policy.UseActions...) {
			if previous, exists := seen[name]; exists {
				return fmt.Errorf("session[%d]: action %q also belongs to session[%d]", i, name, previous)
			}
			seen[name] = i
		}
	}
	return nil
}
