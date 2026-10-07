package s3_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/id"
	reports3 "github.com/defos-ai/commander/providers/s3/reports"
)

func TestReportS3StoreReplayAndImmutability(t *testing.T) {
	f := openStore(t)
	first, err := reports3.NewReportS3Store(f.storage)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reports3.NewReportS3Store(f.storage)
	if err != nil {
		t.Fatal(err)
	}
	report := reportFixture()
	if _, err := first.Get(t.Context(), report.ID); !errors.Is(err, reports.ErrUnknownReport) {
		t.Fatalf("unknown report: %v", err)
	}

	if err := first.Put(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	replay := report.Clone()
	replay.CreatedAt = report.CreatedAt.In(time.FixedZone("other", 3600))
	if err := second.Put(t.Context(), replay); err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	conflict := report.Clone()
	conflict.Output = json.RawMessage(`{"summary":"changed"}`)
	if err := second.Put(t.Context(), conflict); !errors.Is(err, reports.ErrReportConflict) {
		t.Fatalf("different content overwrote report: %v", err)
	}
	stored, err := second.Get(t.Context(), report.ID)
	if err != nil || !reflect.DeepEqual(stored, report) {
		t.Fatalf("report roundtrip: %+v, %v", stored, err)
	}
	stored.Output[0] = '!'
	again, err := first.Get(t.Context(), report.ID)
	if err != nil || !reflect.DeepEqual(again, report) {
		t.Fatalf("reader mutated stored report: %+v, %v", again, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := first.Put(cancelled, reportFixture()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write: %v", err)
	}
	if _, err := first.Get(cancelled, report.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	if _, err := first.Get(t.Context(), "../outside"); err == nil {
		t.Fatal("invalid report ID accepted")
	}
}

func TestConcurrentReportsHaveOneImmutableWinner(t *testing.T) {
	f := openStore(t)
	original := reportFixture()
	changed := original.Clone()
	changed.Output = json.RawMessage(`{"summary":"other candidate"}`)
	const writers = 12
	gate, results := make(chan struct{}), make(chan error, writers)
	for i := range writers {
		store, err := reports3.NewReportS3Store(f.storage)
		if err != nil {
			t.Fatal(err)
		}
		candidate := original
		if i%2 != 0 {
			candidate = changed
		}
		go func() {
			<-gate
			results <- store.Put(t.Context(), candidate)
		}()
	}

	close(gate)
	succeeded, conflicts := 0, 0
	for range writers {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, reports.ErrReportConflict):
			conflicts++
		default:
			t.Errorf("concurrent publication: %v", err)
		}
	}
	if succeeded != writers/2 || conflicts != writers/2 {
		t.Fatalf("immutable winner: successes=%d, conflicts=%d", succeeded, conflicts)
	}
	store, err := reports3.NewReportS3Store(f.storage)
	if err != nil {
		t.Fatal(err)
	}
	winner, err := store.Get(t.Context(), original.ID)
	if err != nil || (!reflect.DeepEqual(winner, original) && !reflect.DeepEqual(winner, changed)) {
		t.Fatalf("invalid winning report: %+v, %v", winner, err)
	}
}

func reportFixture() reports.Report {
	return reports.Report{ID: id.New(), OperationID: id.New(), Mission: "review-website", Output: json.RawMessage(`{"summary":"reviewed","serial":9007199254740993}`), CreatedAt: time.Now().UTC()}
}
