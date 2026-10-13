package missions

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

type RuleName string

const (
	AggregateResults  RuleName = "aggregate-results"
	DecideNextActions RuleName = "decide-next-actions"
)

type Decision string

const Model Decision = "model"

type RuleSet map[RuleName]Rule

type Rule struct {
	ForActions []string    `json:"for_actions"`
	Params     *RuleParams `json:"params,omitempty"`
}

type RuleParams struct {
	DecisionBy Decision `json:"decision_by,omitempty"`
}

func (rules RuleSet) Validate() error {
	for _, name := range slices.Sorted(maps.Keys(rules)) {
		if err := rules[name].validate(name); err != nil {
			return fmt.Errorf("rule %q: %w", name, err)
		}
	}
	return nil
}

func (rule Rule) validate(name RuleName) error {
	if name != AggregateResults && name != DecideNextActions {
		return errors.New("unsupported rule")
	}
	if len(rule.ForActions) == 0 {
		return errors.New("for_actions must not be empty")
	}
	seen := make(map[string]bool, len(rule.ForActions))
	for _, action := range rule.ForActions {
		if strings.TrimSpace(action) == "" || strings.TrimSpace(action) != action {
			return errors.New("for_actions must be nonblank names without surrounding whitespace")
		}
		if seen[action] {
			return fmt.Errorf("duplicate action %q", action)
		}
		seen[action] = true
	}
	if name == AggregateResults && rule.Params != nil && rule.Params.DecisionBy != "" {
		return errors.New("aggregate-results does not accept decision_by")
	}
	if name == DecideNextActions && (rule.Params == nil || rule.Params.DecisionBy != Model) {
		return fmt.Errorf("decision_by must be %q", Model)
	}
	return nil
}

func (rules RuleSet) Clone() RuleSet {
	cloned := maps.Clone(rules)
	for name, rule := range cloned {
		rule.ForActions = slices.Clone(rule.ForActions)
		if rule.Params != nil {
			params := *rule.Params
			rule.Params = &params
		}
		cloned[name] = rule
	}
	return cloned
}
