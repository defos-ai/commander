package missions

import "errors"

type CompletionPolicy struct {
	DecisionBy Decision `json:"decision_by"`
}

func (p CompletionPolicy) Validate() error {
	if p.DecisionBy != Model && p.DecisionBy != Operator {
		return errors.New("completion decision_by must be model or operator")
	}
	return nil
}
