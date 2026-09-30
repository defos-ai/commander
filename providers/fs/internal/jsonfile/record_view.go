package jsonfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// RecordView is valid only inside its View callback. Returned bodies are
// independent copies; reads always use this view's manifest, never the latest.
type RecordView struct {
	records *Records
	refs    []recordRef
	ctx     context.Context
}

func (v *RecordView) Len() int { return len(v.refs) }

func (v *RecordView) Has(name string) bool {
	_, found := v.index(name)
	return found
}

func (v *RecordView) Get(name string) (json.RawMessage, error) {
	if err := v.ctx.Err(); err != nil {
		return nil, err
	}
	index, found := v.index(name)
	if !found {
		return nil, fmt.Errorf("record %q is missing from the manifest", name)
	}
	if err := v.records.checkDirectory(); err != nil {
		return nil, recordReadError(err)
	}
	data, err := v.records.readRecord(v.ctx, v.refs[index].Digest)
	return data, recordReadError(err)
}

type missingRecord struct{ error }

func (e *missingRecord) Unwrap() error { return e.error }

func recordReadError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return &missingRecord{err}
	}
	return err
}

func (v *RecordView) index(name string) (int, bool) {
	return slices.BinarySearchFunc(v.refs, name, func(ref recordRef, name string) int {
		return strings.Compare(ref.Name, name)
	})
}
