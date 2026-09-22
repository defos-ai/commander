package testing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	frameworktesting "commander/framework/testing"
	"commander/providers/internal/jsonfile"
)

// TestFsCleaner clears storage and database records. It never opens the bus.
// Filesystem locks used by live inventory/catalog writers must remain in place.
// Active actions must finish before cleanup; runtime memory is not reset.
type TestFsCleaner struct {
	storage  string
	database string
}

var _ frameworktesting.TestCleaner = (*TestFsCleaner)(nil)

func NewTestFsCleaner(storage, database string) (*TestFsCleaner, error) {
	paths := []*string{&storage, &database}
	for _, path := range paths {
		if strings.TrimSpace(*path) == "" {
			return nil, errors.New("test state directory must not be empty")
		}
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return nil, err
		}
		if filepath.Dir(absolute) == absolute {
			return nil, errors.New("test state directory must not be the filesystem root")
		}
		*path = absolute
	}
	return &TestFsCleaner{storage: storage, database: database}, nil
}

func (c *TestFsCleaner) Clean() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Validate both roots before removing anything. Missing directories are clean.
	for _, directory := range []string{c.storage, c.database} {
		if _, err := readDirectory(directory); err != nil {
			return err
		}
	}
	if err := clearDirectory(c.storage); err != nil {
		return err
	}
	entries, err := readDirectory(c.database)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(c.database, entry.Name())
		switch {
		case strings.HasSuffix(entry.Name(), ".lock"), strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".tmp"):
			// Live catalog writers own their lock and temporary file until rename.
		case entry.Name() == "agents" && entry.IsDir():
			lock, err := jsonfile.NewContext(ctx, filepath.Join(path, ".locks", "inventory.json"))
			if err != nil {
				return err
			}
			if err := lock.View(ctx, func([]byte) error { return clearDirectory(path) }); err != nil {
				return err
			}
		case strings.HasSuffix(entry.Name(), ".json") && entry.Type().IsRegular():
			collection, err := jsonfile.NewContext(ctx, path)
			if err != nil {
				return err
			}
			if err := collection.Update(ctx, func([]byte) ([]byte, error) { return []byte("[]\n"), nil }); err != nil {
				return err
			}
		default:
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
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

// Keep mount points and inventory lock metadata; remove test records and blobs.
func clearDirectory(directory string) error {
	entries, err := readDirectory(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".locks" || strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(directory, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
