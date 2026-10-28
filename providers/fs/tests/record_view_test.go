package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestRecordViewRetriesWholeQueryAfterCollection(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprintf("restore-original=%t", restore), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "records.json")
			reader, writer := fixtures.OpenRecords(t, path), fixtures.OpenRecords(t, path)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			publish := func(generation string) error {
				return writer.Update(ctx, func(next map[string]json.RawMessage) error {
					next["first"], next["second"] = json.RawMessage(generation), json.RawMessage("["+generation+"]")
					return nil
				})
			}
			if err := publish("1"); err != nil {
				t.Fatal(err)
			}
			entered, committed, missing, restored := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			wait := func(signal <-chan struct{}) error {
				select {
				case <-signal:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			done := make(chan error, 1)
			go func() {
				done <- func() error {
					if err := wait(entered); err != nil {
						return err
					}
					for _, generation := range []string{"2", "3"} {
						if err := publish(generation); err != nil {
							return err
						}
					}
					close(committed)
					if restore {
						if err := wait(missing); err != nil {
							return err
						}
						if err := publish("1"); err != nil {
							return err
						}
						close(restored)
					}
					return nil
				}()
			}()
			calls, first, second := 0, "", ""
			err := reader.View(ctx, func(view *jsonfile.RecordView) error {
				calls++
				a, err := view.Get("first")
				if err != nil {
					return err
				}
				if calls == 1 {
					close(entered)
					if err := wait(committed); err != nil {
						return err
					}
				}
				b, err := view.Get("second")
				if calls == 1 && restore {
					close(missing)
					if waitErr := wait(restored); waitErr != nil {
						return waitErr
					}
				}
				first, second = string(a), string(b)
				return err
			})
			if writerErr := <-done; writerErr != nil {
				t.Fatal(writerErr)
			}
			want := "3"
			if restore {
				want = "1"
			}
			if err != nil || calls != 2 || first != want || second != "["+want+"]" {
				t.Fatalf("query must restart from one manifest: calls=%d pair=(%s,%s), want %s; error=%v", calls, first, second, want, err)
			}
		})
	}
}

func TestRecordViewLoadsOnlySelectedBodies(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "records.json")
			records := fixtures.OpenRecords(t, path)
			fixtures.PutRecords(t, records, map[string]json.RawMessage{"selected": json.RawMessage(`1`), "other": json.RawMessage(`2`)})
			var err error
			if damage == "missing" {
				err = os.Remove(fixtures.RecordBlob(path, `2`))
			} else {
				err = os.WriteFile(fixtures.RecordBlob(path, `2`), []byte(`{`), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = records.View(t.Context(), func(view *jsonfile.RecordView) error {
				if view.Len() != 2 || !view.Has("other") || view.Has("absent") {
					t.Error("metadata must reflect the pinned manifest")
				}
				if _, err := view.Get("absent"); err == nil || errors.Is(err, os.ErrNotExist) {
					t.Errorf("logical absence must not look like a collected blob: %v", err)
				}
				body, err := view.Get("selected")
				if string(body) != `1` {
					t.Errorf("selected body = %s", body)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err = records.View(ctx, func(view *jsonfile.RecordView) error { calls++; _, err := view.Get("other"); return err })
			if err == nil || errors.Is(err, os.ErrNotExist) || errors.Is(err, context.DeadlineExceeded) || calls != 1 {
				t.Fatalf("unchanged damaged body must fail promptly: calls=%d, error=%v", calls, err)
			}
		})
	}
}

func TestRecordViewErrorsAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.json")
	records := fixtures.OpenRecords(t, path)
	for _, empty := range []bool{false, true} {
		if empty {
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := records.View(t.Context(), func(*jsonfile.RecordView) error { t.Error("invalid manifest reached callback"); return nil }); err == nil || errors.Is(err, os.ErrNotExist) == empty {
			t.Fatalf("absent manifest must report not-exist; empty file must report corruption: %v", err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	fixtures.PutRecords(t, records, map[string]json.RawMessage{"selected": json.RawMessage(`1`)})
	if err := records.View(t.Context(), nil); err == nil {
		t.Error("nil callback accepted")
	}
	for _, want := range []error{fmt.Errorf("callback: %w", os.ErrNotExist), context.Canceled} {
		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		err := records.View(ctx, func(*jsonfile.RecordView) error {
			calls++
			if want == context.Canceled {
				cancel()
				return nil
			}
			return want
		})
		cancel()
		if !errors.Is(err, want) || calls != 1 {
			t.Fatalf("calls=%d, error=%v, want %v", calls, err, want)
		}
	}
}
