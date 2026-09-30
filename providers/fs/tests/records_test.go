package providers_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"commander/providers/fs/internal/jsonfile"
)

func openRecords(t *testing.T, path string) *jsonfile.Records {
	t.Helper()
	records, err := jsonfile.OpenRecords(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func putRecords(t *testing.T, records *jsonfile.Records, values map[string]json.RawMessage) {
	t.Helper()
	if err := records.Update(t.Context(), func(next map[string]json.RawMessage) error {
		for name, value := range values {
			next[name] = value
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func requireRecords(t *testing.T, records *jsonfile.Records, want map[string]json.RawMessage) {
	t.Helper()
	got, err := records.Read(t.Context())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %s, want %s; error: %v", got, want, err)
	}
}

func recordBlob(path, value string) string {
	return filepath.Join(path+".records", fmt.Sprintf("%x.json", sha256.Sum256([]byte(value))))
}

func TestRecordsConcurrentWritersAndAtomicSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.json")
	first, second := openRecords(t, path), openRecords(t, path)
	putRecords(t, first, map[string]json.RawMessage{"operation": json.RawMessage(`0`), "tasks/id": json.RawMessage(`{"generation":0}`)})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	gate, results := make(chan struct{}), make(chan error, 2)
	for _, records := range []*jsonfile.Records{first, second} {
		go func() {
			<-gate
			for range 25 {
				err := records.Update(ctx, func(next map[string]json.RawMessage) error {
					var count int
					if err := json.Unmarshal(next["operation"], &count); err != nil {
						return err
					}
					next["operation"] = json.RawMessage(fmt.Sprint(count + 1))
					next["tasks/id"] = json.RawMessage(fmt.Sprintf(`{"generation":%d}`, count+1))
					return nil
				})
				if err != nil {
					results <- err
					return
				}
			}
			results <- nil
		}()
	}
	close(gate)
	for completed := 0; completed < 2; {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
			completed++
		default:
			got, err := first.Read(ctx)
			var count int
			var task struct{ Generation int }
			if err != nil || len(got) != 2 || json.Unmarshal(got["operation"], &count) != nil || json.Unmarshal(got["tasks/id"], &task) != nil || count != task.Generation {
				t.Fatalf("reader saw a partial snapshot during replacement and cleanup: %s, %v", got, err)
			}
		}
	}
	requireRecords(t, openRecords(t, path), map[string]json.RawMessage{"operation": json.RawMessage(`50`), "tasks/id": json.RawMessage(`{"generation":50}`)})
}

func TestRecordsReadersAndOtherSetsDoNotWaitForWriter(t *testing.T) {
	dir := t.TempDir()
	first := openRecords(t, filepath.Join(dir, "first.json"))
	reader := openRecords(t, filepath.Join(dir, "first.json"))
	other := openRecords(t, filepath.Join(dir, "other.json"))
	old := map[string]json.RawMessage{"operation": json.RawMessage(`"old"`)}
	putRecords(t, first, old)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	defer func() {
		close(release)
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("writer failed to finish after release")
		}
	}()
	go func() {
		done <- first.Update(t.Context(), func(next map[string]json.RawMessage) error {
			next["operation"] = json.RawMessage(`"new"`)
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("writer never entered callback")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	got, err := reader.Read(ctx)
	if err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("read waited for writer or saw its pending mutation: %s, %v", got, err)
	}
	if err := other.Update(ctx, func(next map[string]json.RawMessage) error {
		next["operation"] = json.RawMessage(`true`)
		return nil
	}); err != nil {
		t.Fatalf("independent set waited for writer: %v", err)
	}
}

func TestRecordsRejectedMutationsLeaveSnapshotUnchanged(t *testing.T) {
	for _, name := range []string{"callback", "invalid JSON", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			records := openRecords(t, filepath.Join(t.TempDir(), "operation.json"))
			old := map[string]json.RawMessage{"operation": json.RawMessage(`{"count":1}`)}
			putRecords(t, records, old)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rejected := errors.New("rejected")
			err := records.Update(ctx, func(next map[string]json.RawMessage) error {
				next["operation"][9] = '2'
				switch name {
				case "callback":
					return rejected
				case "invalid JSON":
					next["tasks/id"] = json.RawMessage(`{`)
				case "cancelled":
					cancel()
				}
				return nil
			})
			if err == nil || errors.Is(err, jsonfile.ErrWriteUncertain) || name == "callback" && !errors.Is(err, rejected) || name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("unexpected rejected mutation result: %v", err)
			}
			requireRecords(t, records, old)
		})
	}
}

func TestRecordsReuseUnchangedFilesAndSkipNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.json")
	records := openRecords(t, path)
	putRecords(t, records, map[string]json.RawMessage{"operation": json.RawMessage(`null`), "tasks/id": json.RawMessage(`[1]`)})
	blob, err := os.Stat(recordBlob(path, `null`))
	if err != nil {
		t.Fatal(err)
	}
	putRecords(t, records, map[string]json.RawMessage{"tasks/id": json.RawMessage(`[2]`)})
	after, err := os.Stat(recordBlob(path, `null`))
	if err != nil || !os.SameFile(blob, after) || !blob.ModTime().Equal(after.ModTime()) {
		t.Fatalf("unchanged record was rewritten: %v", err)
	}
	manifest, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	putRecords(t, records, nil)
	after, err = os.Stat(path)
	if err != nil || !os.SameFile(manifest, after) || !manifest.ModTime().Equal(after.ModTime()) {
		t.Fatalf("no-op update replaced the manifest: %v", err)
	}
	if err := records.Update(t.Context(), func(next map[string]json.RawMessage) error { delete(next, "tasks/id"); return nil }); err != nil {
		t.Fatal(err)
	}
	requireRecords(t, openRecords(t, path), map[string]json.RawMessage{"operation": json.RawMessage(`null`)})
}

func TestRecordsRepeatedContentAdvancesManifestRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.json")
	records := openRecords(t, path)
	var previousVersion uint64
	var initialRefs json.RawMessage
	for index, value := range []string{`0`, `1`, `0`} {
		putRecords(t, records, map[string]json.RawMessage{"operation": json.RawMessage(value)})
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var manifest []struct {
			Version uint64          `json:"version"`
			Records json.RawMessage `json:"records"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil || len(manifest) != 1 || manifest[0].Version <= previousVersion {
			t.Fatalf("each publication needs a distinct increasing revision: %s, %v", data, err)
		}
		previousVersion = manifest[0].Version
		if index == 0 {
			initialRefs = manifest[0].Records
		} else if index == 2 && !bytes.Equal(initialRefs, manifest[0].Records) {
			t.Fatalf("restoring the same content changed its references: %s", manifest[0].Records)
		}
	}
	requireRecords(t, records, map[string]json.RawMessage{"operation": json.RawMessage(`0`)})
}

func TestRecordsNoOpRetryConfirmsDirectoryDurability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "operation.json")
	records := openRecords(t, path)
	putRecords(t, records, map[string]json.RawMessage{"operation": json.RawMessage(`1`)})
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	restore := denyDirectoryRead(t, dir)
	err = records.Update(t.Context(), func(map[string]json.RawMessage) error { return nil })
	restore()
	if !errors.Is(err, jsonfile.ErrWriteUncertain) || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("no-op retry must report failure to confirm manifest durability: %v", err)
	}
	putRecords(t, records, nil)
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("confirming durability replaced the manifest: %v", err)
	}
	confirmed, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, confirmed) {
		t.Fatalf("confirming durability changed the snapshot or revision: %s, %v", confirmed, err)
	}
}

func TestRecordsStagingFailurePreservesSnapshotAndRetryCleansOrphans(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.json")
	records := openRecords(t, path)
	old := map[string]json.RawMessage{"operation": json.RawMessage(`0`), "tasks/id": json.RawMessage(`1`)}
	putRecords(t, records, old)
	blocked := recordBlob(path, `3`)
	err := records.Update(t.Context(), func(next map[string]json.RawMessage) error {
		next["operation"], next["tasks/id"] = json.RawMessage(`2`), json.RawMessage(`3`)
		return os.Mkdir(blocked, 0700)
	})
	if err == nil || errors.Is(err, jsonfile.ErrWriteUncertain) {
		t.Fatalf("staging failure must precede publication: %v", err)
	}
	requireRecords(t, records, old)
	orphan := recordBlob(path, `2`)
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("first changed blob was not staged before the second failed: %v", err)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(path+".records", ".interrupted.tmp")
	if err := os.WriteFile(pending, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened := openRecords(t, path)
	putRecords(t, reopened, map[string]json.RawMessage{"operation": json.RawMessage(`4`), "tasks/id": json.RawMessage(`5`)})
	requireRecords(t, openRecords(t, path), map[string]json.RawMessage{"operation": json.RawMessage(`4`), "tasks/id": json.RawMessage(`5`)})
	for _, file := range []string{orphan, pending} {
		if _, err := os.Lstat(file); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("successful retry did not remove staged orphan %s: %v", file, err)
		}
	}
}

func TestRecordsMissingOrCorruptReferencedBlobFailsPromptly(t *testing.T) {
	for _, damage := range []string{"missing", "invalid JSON", "wrong digest"} {
		t.Run(damage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "operation.json")
			records := openRecords(t, path)
			if _, err := records.Read(t.Context()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing manifest must report not-exist: %v", err)
			}
			putRecords(t, records, map[string]json.RawMessage{"operation": json.RawMessage(`1`)})
			file := recordBlob(path, `1`)
			var err error
			if damage == "missing" {
				err = os.Remove(file)
			} else {
				data := []byte(`2`)
				if damage == "invalid JSON" {
					data = []byte(`{`)
				}
				err = os.WriteFile(file, data, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := records.Read(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unchanged corrupt manifest must fail without retrying until cancellation: %v", err)
			}
			called := false
			if err := records.Update(ctx, func(map[string]json.RawMessage) error { called = true; return nil }); err == nil || called {
				t.Fatalf("corrupt data reached mutation: called=%v, error=%v", called, err)
			}
		})
	}
}

func TestRecordsRejectUnsafeManifestDigests(t *testing.T) {
	for _, digest := range []string{"../outside", "", strings.Repeat("z", 64)} {
		path := filepath.Join(t.TempDir(), "operation.json")
		records := openRecords(t, path)
		data, err := json.Marshal([]map[string]any{{"version": 1, "records": []map[string]string{{"name": "operation", "digest": digest}}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := records.Read(t.Context()); err == nil || errors.Is(err, os.ErrNotExist) {
			t.Errorf("unsafe digest %q must be rejected before opening a blob: %v", digest, err)
		}
	}
}
