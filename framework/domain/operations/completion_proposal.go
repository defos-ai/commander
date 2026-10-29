package operations

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/id"
)

type CompletionProposal struct {
	ID         string           `json:"id"`
	Output     json.RawMessage  `json:"output"`
	ProposedBy CompletionOrigin `json:"proposed_by"`
	ProposedAt time.Time        `json:"proposed_at"`
}

func (p CompletionProposal) Validate() error {
	if !id.Valid(p.ID) {
		return errors.New("completion proposal ID must be a canonical UUID")
	}
	if !utf8.Valid(p.Output) || !json.Valid(p.Output) {
		return errors.New("completion proposal output must be valid JSON")
	}
	if err := p.ProposedBy.Validate(); err != nil {
		return err
	}
	if p.ProposedAt.IsZero() {
		return errors.New("completion proposal requires its submission time")
	}
	return nil
}

func (p CompletionProposal) Clone() CompletionProposal {
	p.Output = bytes.Clone(p.Output)
	return p
}

func (o *Operation) ProposeCompletion(proposal CompletionProposal) error {
	if o.Status.Terminal() {
		return ErrFinished
	}
	if err := proposal.Validate(); err != nil {
		return err
	}
	if prior := o.CompletionProposal; prior != nil && prior.ID == proposal.ID {
		if !sameCompletionProposal(*prior, proposal) {
			return ErrOperationConflict
		}
		return nil
	}
	if err := o.ValidateCompletionCandidate(proposal.Output); err != nil {
		return err
	}
	copy := proposal.Clone()
	o.CompletionProposal = &copy
	o.AppendEvent(CompletionProposed, "", "", "Final output proposed for review")
	o.UpdatedAt = time.Now().UTC()
	return nil
}

func sameCompletionProposal(a, b CompletionProposal) bool {
	// Operation envelopes compact RawMessage whitespace and escape HTML.
	first, firstErr := json.Marshal(a.Output)
	second, secondErr := json.Marshal(b.Output)
	return firstErr == nil && secondErr == nil && bytes.Equal(first, second) &&
		a.ProposedBy == b.ProposedBy && a.ProposedAt.Equal(b.ProposedAt)
}
