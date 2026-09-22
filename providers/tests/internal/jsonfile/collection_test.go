package jsonfile_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"commander/providers/internal/jsonfile"
)

func newTestCollection(t *testing.T) *jsonfile.Collection {
	t.Helper()
	c, err := jsonfile.New(filepath.Join(t.TempDir(), "storage", "records.json"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func collectionBytes(t *testing.T, c *jsonfile.Collection) []byte {
	t.Helper()
	data, err := os.ReadFile(c.Path())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestNewInitializesAnEmptyArray(t *testing.T) {
	// Arrange.
	path := filepath.Join(t.TempDir(), "storage", "records.json")

	// Act.
	c, err := jsonfile.New(path)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes.TrimSpace(collectionBytes(t, c))) != "[]" {
		t.Fatal("new collection was not initialized as an empty array")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(c.Path())
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("collection permission = %v, error=%v", info, err)
		}
	}
}

func TestReopeningPreservesExistingRecords(t *testing.T) {
	// Arrange.
	c := newTestCollection(t)
	if err := c.Update(context.Background(), func([]byte) ([]byte, error) { return []byte(`["saved"]`), nil }); err != nil {
		t.Fatal(err)
	}

	// Act.
	other, err := jsonfile.New(c.Path())

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if string(collectionBytes(t, other)) != `["saved"]` {
		t.Fatal("reopening replaced existing records")
	}
}

func TestMissingCollectionIsAnEmptyArray(t *testing.T) {
	for _, operation := range []string{"view", "update"} {
		t.Run(operation, func(t *testing.T) {
			// Arrange.
			c := newTestCollection(t)
			if err := os.Remove(c.Path()); err != nil {
				t.Fatal(err)
			}
			var observed []byte
			var err error

			// Act.
			if operation == "view" {
				err = c.View(context.Background(), func(data []byte) error { observed = data; return nil })
			} else {
				err = c.Update(context.Background(), func(data []byte) ([]byte, error) { observed = data; return data, nil })
			}

			// Assert.
			if err != nil || string(observed) != "[]" {
				t.Fatalf("missing collection = %s, error=%v", observed, err)
			}
			if operation == "view" {
				if _, err := os.Stat(c.Path()); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("view created a collection: %v", err)
				}
			} else if string(collectionBytes(t, c)) != "[]" {
				t.Fatal("update did not recreate the empty collection")
			}
		})
	}
}

func TestFailedUpdatesPreservePreviousBytes(t *testing.T) {
	sentinel := errors.New("encoding failed")
	for _, test := range []struct {
		name, result string
		callbackErr  error
		cancel       bool
		wantErr      error
	}{
		{name: "callback error", callbackErr: sentinel, wantErr: sentinel},
		{name: "cancelled callback", result: `["cancelled"]`, cancel: true, wantErr: context.Canceled},
		{name: "empty output"},
		{name: "null", result: "null"},
		{name: "object", result: "{}"},
		{name: "incomplete array", result: "["},
		{name: "multiple arrays", result: "[] []"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			c := newTestCollection(t)
			before := collectionBytes(t, c)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Act.
			err := c.Update(ctx, func(data []byte) ([]byte, error) {
				data[0] = 'x'
				if test.cancel {
					cancel()
				}
				return []byte(test.result), test.callbackErr
			})

			// Assert.
			if err == nil || (test.wantErr != nil && !errors.Is(err, test.wantErr)) {
				t.Fatalf("update error = %v, want %v", err, test.wantErr)
			}
			if !bytes.Equal(before, collectionBytes(t, c)) {
				t.Fatal("failed update changed existing bytes")
			}
			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(c.Path()), ".*.tmp"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("temporary files survived failed updates: %v, %v", leftovers, err)
			}
		})
	}
}

func TestCorruptCollectionsAreNeverOverwritten(t *testing.T) {
	for _, operation := range []string{"open", "update"} {
		t.Run(operation, func(t *testing.T) {
			// Arrange.
			c := newTestCollection(t)
			before := []byte("[corrupt")
			if err := os.WriteFile(c.Path(), before, 0600); err != nil {
				t.Fatal(err)
			}
			called := false
			var err error

			// Act.
			if operation == "open" {
				_, err = jsonfile.New(c.Path())
			} else {
				err = c.Update(context.Background(), func([]byte) ([]byte, error) { called = true; return []byte("[]"), nil })
			}

			// Assert.
			if err == nil || called {
				t.Fatalf("corruption did not block operation: called=%v error=%v", called, err)
			}
			if !bytes.Equal(before, collectionBytes(t, c)) {
				t.Fatal("corrupt content was overwritten")
			}
		})
	}
}

func TestIndependentInstancesSerializeAndWaitingContextCancels(t *testing.T) {
	// Arrange: keep one handle inside an update while the other waits.
	c := newTestCollection(t)
	other, err := jsonfile.New(c.Path())
	if err != nil {
		t.Fatal(err)
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- c.Update(context.Background(), func([]byte) ([]byte, error) {
			close(entered)
			<-release
			return []byte(`["first"]`), nil
		})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	called := false

	// Act.
	err = other.Update(ctx, func(data []byte) ([]byte, error) { called = true; return data, nil })
	close(release)
	holderErr := <-finished

	// Assert.
	if holderErr != nil {
		t.Fatal(holderErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatalf("waiting update did not cancel before callback: error=%v called=%v", err, called)
	}
	if data := collectionBytes(t, other); string(data) != `["first"]` {
		t.Fatalf("lock holder's commit was lost: %s", data)
	}
}

func TestNewContextCancelsWhileAnotherInstanceHoldsLock(t *testing.T) {
	// Arrange.
	c := newTestCollection(t)
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- c.View(context.Background(), func([]byte) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	// Act.
	_, err := jsonfile.NewContext(ctx, c.Path())
	close(release)
	holderErr := <-finished

	// Assert.
	if holderErr != nil {
		t.Fatal(holderErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("constructor did not cancel while waiting: %v", err)
	}
}

func TestReadDoesNotInitializeMissingCollection(t *testing.T) {
	// Arrange.
	path := filepath.Join(t.TempDir(), "missing", "records.json")

	// Act.
	_, err := jsonfile.Read(context.Background(), path)

	// Assert.
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing read: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created directories: %v", err)
	}
}

func TestAtomicReadersAndConcurrentWriters(t *testing.T) {
	// Arrange.
	c := newTestCollection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	readerDone := make(chan error, 1)
	stopReader := make(chan struct{})
	const writers, updates = 4, 20
	results := make(chan error, writers)
	var work sync.WaitGroup

	// Act.
	go func() {
		for {
			select {
			case <-stopReader:
				readerDone <- nil
				return
			case <-ctx.Done():
				readerDone <- ctx.Err()
				return
			default:
			}
			data, err := jsonfile.Read(ctx, c.Path())
			if err != nil || !json.Valid(data) {
				readerDone <- fmt.Errorf("reader observed partial or missing JSON: %v", err)
				return
			}
		}
	}()
	for range writers {
		work.Go(func() {
			instance, err := jsonfile.New(c.Path())
			if err == nil {
				for range updates {
					if err = appendRecord(ctx, instance); err != nil {
						break
					}
				}
			}
			results <- err
		})
	}
	work.Wait()
	close(stopReader)
	readerErr := <-readerDone

	// Assert.
	if readerErr != nil {
		t.Fatal(readerErr)
	}
	for range writers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var records []string
	if err := json.Unmarshal(collectionBytes(t, c), &records); err != nil || len(records) != writers*updates {
		t.Fatalf("concurrent updates were lost: count=%d error=%v", len(records), err)
	}
}

func appendRecord(ctx context.Context, c *jsonfile.Collection) error {
	return c.Update(ctx, func(data []byte) ([]byte, error) {
		var records []string
		if err := json.Unmarshal(data, &records); err != nil {
			return nil, err
		}
		return json.Marshal(append(records, strings.Repeat("entry", 200)))
	})
}

func TestRejectsSymlinkAndNonregularPaths(t *testing.T) {
	for _, target := range []string{"collection", "lock"} {
		for _, kind := range []string{"symlink", "directory"} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				// Arrange.
				root := t.TempDir()
				path := filepath.Join(root, "records.json")
				badPath := path
				if target == "lock" {
					badPath += ".lock"
				}
				outside := filepath.Join(root, "outside.json")
				if err := os.WriteFile(outside, []byte(`["untouched"]`), 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					if err := os.Symlink(outside, badPath); err != nil {
						if runtime.GOOS == "windows" {
							t.Skip("creating symlinks requires Windows developer mode or privilege")
						}
						t.Fatal(err)
					}
				} else if err := os.Mkdir(badPath, 0700); err != nil {
					t.Fatal(err)
				}

				// Act.
				_, err := jsonfile.New(path)

				// Assert.
				if err == nil {
					t.Fatal("nonregular path was accepted")
				}
				data, err := os.ReadFile(outside)
				if err != nil || string(data) != `["untouched"]` {
					t.Fatal("symlink target was changed")
				}
			})
		}
	}
}

// This subprocess fixture verifies OS file locks through the public API.
func TestJSONFileProcessHelper(t *testing.T) {
	// Arrange.
	if os.Getenv("COMMANDER_JSONFILE_HELPER") != "1" {
		t.Skip("subprocess fixture")
	}
	c, err := jsonfile.New(os.Getenv("COMMANDER_JSONFILE_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Act.
	for range 15 {
		if err = appendRecord(ctx, c); err != nil {
			break
		}
	}

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
}

func TestExistingCollectionRejectsReplacedPaths(t *testing.T) {
	for _, lockPath := range []bool{false, true} {
		// Arrange.
		c := newTestCollection(t)
		outside := filepath.Join(filepath.Dir(c.Path()), "outside.json")
		if err := os.WriteFile(outside, []byte(`["untouched"]`), 0600); err != nil {
			t.Fatal(err)
		}
		path := c.Path()
		if lockPath {
			path += ".lock"
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, path); err != nil {
			if runtime.GOOS == "windows" {
				t.Skip("creating symlinks requires Windows developer mode or privilege")
			}
			t.Fatal(err)
		}
		called := false

		// Act.
		viewErr := c.View(context.Background(), func([]byte) error { called = true; return nil })
		updateErr := c.Update(context.Background(), func([]byte) ([]byte, error) { called = true; return []byte("[]"), nil })

		// Assert.
		if viewErr == nil || updateErr == nil || called {
			t.Fatalf("replaced path accepted: view=%v update=%v callback=%v", viewErr, updateErr, called)
		}
		if data, err := os.ReadFile(outside); err != nil || string(data) != `["untouched"]` {
			t.Fatal("replaced path changed its symlink target")
		}
	}
}

func TestSeparateProcessesSerializeUpdates(t *testing.T) {
	// Arrange.
	c := newTestCollection(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const processes = 3
	results := make(chan error, processes)

	// Act.
	for range processes {
		go func() {
			command := exec.CommandContext(ctx, executable, "-test.run=^TestJSONFileProcessHelper$")
			command.Env = append(os.Environ(), "COMMANDER_JSONFILE_HELPER=1", "COMMANDER_JSONFILE_PATH="+c.Path())
			output, err := command.CombinedOutput()
			if err != nil {
				err = fmt.Errorf("helper failed: %w: %s", err, output)
			}
			results <- err
		}()
	}
	errors := make([]error, processes)
	for index := range errors {
		errors[index] = <-results
	}

	// Assert.
	for _, err := range errors {
		if err != nil {
			t.Error(err)
		}
	}
	var records []string
	if err := json.Unmarshal(collectionBytes(t, c), &records); err != nil || len(records) != processes*15 {
		t.Fatalf("cross-process updates were lost: count=%d error=%v", len(records), err)
	}
}
