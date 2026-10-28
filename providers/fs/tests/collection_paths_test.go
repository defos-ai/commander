package providers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestCollectionRejectsSymlinkedFilesAndLocks(t *testing.T) {
	for _, target := range []string{"collection", "lock"} {
		t.Run(target, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data.json")
			collection, err := jsonfile.New(path)
			if err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "outside.json")
			if err := os.WriteFile(outside, []byte(`["outside"]`), 0600); err != nil {
				t.Fatal(err)
			}
			link := path
			if target == "lock" {
				link += ".lock"
			}
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			fixtures.Link(t, outside, link)
			called := false
			err = collection.Update(t.Context(), func([]byte) ([]byte, error) {
				called = true
				return []byte(`["changed"]`), nil
			})
			if err == nil || called {
				t.Fatalf("symlink reached mutation: called=%v, error=%v", called, err)
			}
			if target == "collection" {
				if _, err := jsonfile.Read(t.Context(), path); err == nil {
					t.Fatal("read followed a symlink outside the collection directory")
				}
			}
			assertFileBytes(t, outside, []byte(`["outside"]`))
		})
	}
}

func TestCollectionPinsDirectoryDuringMutation(t *testing.T) {
	parent := t.TempDir()
	directory, moved := filepath.Join(parent, "data"), filepath.Join(parent, "moved")
	path := filepath.Join(directory, "collection.json")
	collection, err := jsonfile.New(path)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "collection.json")
	if err := os.WriteFile(outsidePath, []byte(`["outside"]`), 0600); err != nil {
		t.Fatal(err)
	}
	var renameErr error
	err = collection.Update(t.Context(), func([]byte) ([]byte, error) {
		renameErr = os.Rename(directory, moved)
		if renameErr != nil {
			return nil, renameErr
		}
		fixtures.Link(t, outside, directory)
		return []byte(`["updated"]`), nil
	})
	assertFileBytes(t, outsidePath, []byte(`["outside"]`))
	if directoryReplacementBlocked(t, err, renameErr) {
		assertFileBytes(t, path, []byte("[]\n"))
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(moved, "collection.json"), []byte(`["updated"]`))
	if err := collection.View(t.Context(), func([]byte) error { return nil }); err == nil {
		t.Fatal("subsequent read accepted a replaced collection directory")
	}
}

func TestRecordsPinsManifestDirectoryDuringMutation(t *testing.T) {
	parent := t.TempDir()
	directory, moved := filepath.Join(parent, "data"), filepath.Join(parent, "moved")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "operation.json")
	records := fixtures.OpenRecords(t, path)
	fixtures.PutRecords(t, records, map[string]json.RawMessage{"operation": json.RawMessage(`1`)})
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "operation.json")
	if err := os.WriteFile(outsidePath, []byte(`["outside"]`), 0600); err != nil {
		t.Fatal(err)
	}
	var renameErr error
	err := records.Update(t.Context(), func(next map[string]json.RawMessage) error {
		renameErr = os.Rename(directory, moved)
		if renameErr != nil {
			return renameErr
		}
		fixtures.Link(t, outside, directory)
		next["operation"] = json.RawMessage(`2`)
		return nil
	})
	assertFileBytes(t, outsidePath, []byte(`["outside"]`))
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 1 {
		t.Fatalf("record staging escaped its directory: entries=%v, error=%v", entries, err)
	}
	if directoryReplacementBlocked(t, err, renameErr) {
		requireRecords(t, records, map[string]json.RawMessage{"operation": json.RawMessage(`1`)})
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	requireRecords(t, fixtures.OpenRecords(t, filepath.Join(moved, "operation.json")), map[string]json.RawMessage{"operation": json.RawMessage(`2`)})
}

func TestRecordsRejectsReplacedBlobDirectoryBeforeCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.json")
	records := fixtures.OpenRecords(t, path)
	fixtures.PutRecords(t, records, map[string]json.RawMessage{"operation": json.RawMessage(`1`)})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	orphan := filepath.Base(fixtures.RecordBlob(path, `7`))
	for _, name := range []string{orphan, ".pending.tmp"} {
		if err := os.WriteFile(filepath.Join(outside, name), []byte(`7`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	err = records.Update(t.Context(), func(next map[string]json.RawMessage) error {
		if err := os.Rename(path+".records", path+".saved"); err != nil {
			return err
		}
		fixtures.Link(t, outside, path+".records")
		next["operation"] = json.RawMessage(`2`)
		return nil
	})
	if err == nil || errors.Is(err, jsonfile.ErrWriteUncertain) {
		t.Fatalf("replaced blob directory must fail before publication: %v", err)
	}
	assertFileBytes(t, path, before)
	for _, name := range []string{orphan, ".pending.tmp"} {
		assertFileBytes(t, filepath.Join(outside, name), []byte(`7`))
	}
	if _, err := records.Read(t.Context()); err == nil {
		t.Fatal("read accepted a symlinked blob directory")
	}
}

func TestRecordsRejectsSymlinkedBlob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.json")
	records := fixtures.OpenRecords(t, path)
	fixtures.PutRecords(t, records, map[string]json.RawMessage{"operation": json.RawMessage(`1`)})
	blob := fixtures.RecordBlob(path, `1`)
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`1`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blob); err != nil {
		t.Fatal(err)
	}
	fixtures.Link(t, outside, blob)
	if _, err := records.Read(t.Context()); err == nil {
		t.Fatal("read followed a symlinked blob despite matching content")
	}
}

func directoryReplacementBlocked(t *testing.T, operationErr, renameErr error) bool {
	t.Helper()
	// Windows may deny renaming an open root instead of following its new location.
	if runtime.GOOS != "windows" || renameErr == nil {
		return false
	}
	if !errors.Is(operationErr, renameErr) || errors.Is(operationErr, jsonfile.ErrWriteUncertain) {
		t.Fatalf("blocked directory replacement must abort before publication: %v", operationErr)
	}
	t.Logf("open root prevented directory replacement: %v", renameErr)
	return true
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("file %s changed: got=%s, want=%s, error=%v", path, got, want, err)
	}
}
