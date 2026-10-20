package executions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/domain"
)

const (
	MaxCheckpoints          = 32
	MaxCheckpointSize       = 16 * 1024
	MaxCheckpointNameLength = 64
)

// ValidateCheckpoint bounds execution metadata. Checkpoints contain intermediate
// facts, never file contents or secrets. Their names are local to one execution.
func ValidateCheckpoint(name string, data json.RawMessage) error {
	if len(name) == 0 || len(name) > MaxCheckpointNameLength || name[0] < 'a' || name[0] > 'z' {
		return errors.New("checkpoint name must start with a lowercase letter and contain at most 64 characters")
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return errors.New("checkpoint name may contain only lowercase letters, digits, hyphens and underscores")
		}
	}
	if len(data) > MaxCheckpointSize {
		return fmt.Errorf("checkpoint data must not exceed %d bytes", MaxCheckpointSize)
	}
	if !utf8.Valid(data) {
		return errors.New("checkpoint data must be valid UTF-8")
	}
	if err := domain.ValidateObject(data); err != nil {
		return errors.New("checkpoint data must be a JSON object")
	}
	// A JSON envelope can expand HTML characters and Unicode line separators.
	// Bound that representation too so saving cannot make a checkpoint unreadable.
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) > MaxCheckpointSize {
		return fmt.Errorf("encoded checkpoint data must not exceed %d bytes", MaxCheckpointSize)
	}
	return nil
}

// RecordCheckpoint adds an immutable fact and reports whether it was new.
// Identical retries confirm the existing value, including after completion.
// Object key order and whitespace do not matter; number precision is preserved.
// Persist the updated execution atomically before acknowledging this operation.
func (r *Execution) RecordCheckpoint(name string, data json.RawMessage) (bool, error) {
	if err := r.Validate(); err != nil {
		return false, err
	}
	if err := ValidateCheckpoint(name, data); err != nil {
		return false, err
	}
	if saved, exists := r.Checkpoints[name]; exists {
		if !sameCheckpoint(saved, data) {
			return false, ErrCheckpointConflict
		}
		return false, nil
	}
	if r.Result != nil {
		return false, ErrExecutionCompleted
	}
	if len(r.Checkpoints) >= MaxCheckpoints {
		return false, fmt.Errorf("execution cannot contain more than %d checkpoints", MaxCheckpoints)
	}
	if r.Checkpoints == nil {
		r.Checkpoints = make(map[string]json.RawMessage)
	}
	r.Checkpoints[name] = bytes.Clone(data)
	return true, nil
}

func sameCheckpoint(a, b json.RawMessage) bool {
	decode := func(data json.RawMessage) any {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		// Both objects have already passed validation.
		_ = decoder.Decode(&value)
		return value
	}
	return reflect.DeepEqual(decode(a), decode(b))
}
