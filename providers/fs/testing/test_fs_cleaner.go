package testing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"commander/framework/id"
	frameworktesting "commander/framework/testing"
	"commander/providers/fs/internal/jsonfile"
)

// TestFsCleaner clears only public operation/artifact records and report/artifact
// files. Catalogs, executions, and persistent locks remain in place. Compose it
// with framework/testing.StateCleaner to check that test work has settled.
type TestFsCleaner struct {
	storage  string
	database string
}

var _ frameworktesting.TestCleaner = (*TestFsCleaner)(nil)

func NewTestFsCleaner(storage, database string) (*TestFsCleaner, error) {
	storageCleaner, err := NewTestFsStorageCleaner(storage)
	if err != nil {
		return nil, err
	}
	databaseCleaner, err := NewTestFsDatabaseCleaner(database)
	if err != nil {
		return nil, err
	}
	return &TestFsCleaner{storage: storageCleaner.storage, database: databaseCleaner.database}, nil
}

func NewTestFsStorageCleaner(storage string) (*TestFsCleaner, error) {
	path, err := testDirectory(storage)
	if err != nil {
		return nil, err
	}
	return &TestFsCleaner{storage: path}, nil
}

func NewTestFsDatabaseCleaner(database string) (*TestFsCleaner, error) {
	path, err := testDirectory(database)
	if err != nil {
		return nil, err
	}
	return &TestFsCleaner{database: path}, nil
}

func testDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("test state directory must not be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if filepath.Dir(absolute) == absolute {
		return "", errors.New("test state directory must not be the filesystem root")
	}
	return absolute, nil
}

func (c *TestFsCleaner) Clean() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.CleanContext(ctx)
}

func (c *TestFsCleaner) CleanContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("test filesystem cleanup requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.storage == "" && c.database == "" {
		return errors.New("test filesystem cleanup requires a configured directory")
	}
	// Validate all roots and allowed namespaces before removing anything. Missing
	// directories are clean; symlinked roots or namespaces are rejected.
	for _, root := range []struct {
		path  string
		names []string
	}{{c.database, []string{"operations", "artifacts"}}, {c.storage, []string{"artifacts", "reports"}}} {
		if root.path == "" {
			continue
		}
		for _, name := range append([]string{""}, root.names...) {
			if _, err := readDirectory(filepath.Join(root.path, name)); err != nil {
				return err
			}
		}
	}
	if c.database != "" {
		if err := clearRecords(ctx, filepath.Join(c.database, "operations"), true); err != nil {
			return err
		}
		if err := clearRecords(ctx, filepath.Join(c.database, "artifacts"), false); err != nil {
			return err
		}
	}
	if c.storage != "" {
		for _, name := range []string{"artifacts", "reports"} {
			if err := clearDirectory(ctx, filepath.Join(c.storage, name)); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func clearRecords(ctx context.Context, directory string, operationBlobs bool) error {
	entries, err := readDirectory(directory)
	if err != nil {
		return err
	}
	cleared := make(map[string]bool)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".json") && entry.Type().IsRegular():
		case operationBlobs && strings.HasSuffix(name, ".json.records") && entry.IsDir():
			// Interrupted creates can leave blobs without a published manifest.
			name = strings.TrimSuffix(name, ".records")
		default:
			continue
		}
		if !id.Valid(strings.TrimSuffix(name, ".json")) || cleared[name] {
			continue
		}
		path := filepath.Join(directory, name)
		file, err := jsonfile.OpenContext(ctx, path)
		if err != nil {
			return err
		}
		if !operationBlobs {
			// Keep the ID under the same metadata lock so delayed result
			// redelivery cannot recreate an artifact after its bytes are removed.
			data, err := json.Marshal([]struct {
				ID          string `json:"id"`
				TestDeleted bool   `json:"test_deleted"`
			}{{ID: strings.TrimSuffix(name, ".json"), TestDeleted: true}})
			if err != nil {
				return err
			}
			if err := file.Update(ctx, func([]byte) ([]byte, error) { return data, nil }); err != nil {
				return err
			}
			continue
		}
		if err := file.View(ctx, func([]byte) error {
			// Unpublish the snapshot before removing its blobs, while keeping
			// the writer's persistent lock in place.
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return os.RemoveAll(path + ".records")
		}); err != nil {
			return err
		}
		cleared[name] = true
	}
	return nil
}

func readDirectory(directory string) ([]os.DirEntry, error) {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("test state must be a directory, not a symlink or file")
	}
	return os.ReadDir(directory)
}

// Keep directories and locks in place while removing files from the explicitly
// selected artifact/report namespace. This never visits the configured root.
func clearDirectory(ctx context.Context, directory string) error {
	entries, err := readDirectory(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Name() == ".locks" || strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if entry.IsDir() {
			if err := clearDirectory(ctx, path); err != nil {
				return err
			}
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
