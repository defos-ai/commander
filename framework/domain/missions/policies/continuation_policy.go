package policies

import "slices"

type ContinuationPolicy struct {
	// AggregateResults groups the saved direct children of each matching execution.
	AggregateResults []string `json:"aggregate_results,omitempty"`
	// ModelReview forces review even when successful tasks propose valid next actions.
	ModelReview []string `json:"model_review,omitempty"`
}

func (p ContinuationPolicy) Validate() error {
	if err := validateActionNames("aggregate_results", p.AggregateResults); err != nil {
		return err
	}
	return validateActionNames("model_review", p.ModelReview)
}

func (p ContinuationPolicy) Clone() ContinuationPolicy {
	p.AggregateResults = slices.Clone(p.AggregateResults)
	p.ModelReview = slices.Clone(p.ModelReview)
	return p
}
