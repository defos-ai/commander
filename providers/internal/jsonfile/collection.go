// Package jsonfile provides locked, atomic JSON array collections. The sibling
// .lock file is persistent; its operating-system lock is released even on crash.
package jsonfile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Collection struct{ path string }

var ErrWriteUncertain = errors.New("JSON collection was replaced but directory sync failed")

// New creates parent directories and initializes a missing collection to [].
// Existing content is validated and never overwritten during construction.
func New(path string) (*Collection, error) {
	return NewContext(context.Background(), path)
}

// NewContext is New with cancellation while initializing or waiting for a lock.
func NewContext(ctx context.Context, path string) (*Collection, error) {
	c, err := OpenContext(ctx, path)
	if err != nil {
		return nil, err
	}
	err = c.locked(ctx, func() error {
		_, exists, err := c.read(ctx)
		if err != nil || exists {
			return err
		}
		return c.write(ctx, []byte("[]\n"))
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// OpenContext prepares a collection without publishing an empty file. The first
// Update can therefore commit a complete record in a single atomic replacement.
func OpenContext(ctx context.Context, path string) (*Collection, error) {
	if ctx == nil {
		return nil, errors.New("JSON collection requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("JSON collection path must not be blank")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	// Resolve directory aliases so independent instances use the same lock.
	directory, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	c := &Collection{path: filepath.Join(directory, filepath.Base(absolute))}
	if err := regularPath(c.path); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Collection) Path() string { return c.path }

// Read returns an atomic array snapshot without locking or creating any paths.
// It rejects symlinks and reports os.ErrNotExist for a missing collection.
func Read(ctx context.Context, path string) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("JSON collection requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, exists, err := (&Collection{path: path}).read(ctx)
	if err == nil && !exists {
		err = &os.PathError{Op: "read", Path: path, Err: os.ErrNotExist}
	}
	return data, err
}

// View supplies an independent snapshot under the collection lock. A missing
// file is an empty collection, including if it was removed after construction.
// Callbacks must not reenter this collection or wait for another writer to it.
func (c *Collection) View(ctx context.Context, fn func([]byte) error) error {
	return c.locked(ctx, func() error {
		data, _, err := c.read(ctx)
		if err != nil {
			return err
		}
		if err := fn(data); err != nil {
			return err
		}
		return ctx.Err()
	})
}

// Update serializes read-modify-write across instances and processes. Callback,
// validation, and pre-commit cancellation failures leave the old file intact.
// Once the rename commits, cancellation does not change a successful result.
func (c *Collection) Update(ctx context.Context, fn func([]byte) ([]byte, error)) error {
	return c.locked(ctx, func() error {
		data, _, err := c.read(ctx)
		if err != nil {
			return err
		}
		data, err = fn(data)
		if err != nil {
			return err
		}
		if err := validate(data); err != nil {
			return err
		}
		return c.write(ctx, data)
	})
}

func (c *Collection) locked(ctx context.Context, fn func() error) error {
	if ctx == nil {
		return errors.New("JSON collection requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	lockPath := c.path + ".lock"
	file, err := openRegular(lockPath, true)
	if err != nil {
		return fmt.Errorf("open collection lock: %w", err)
	}
	defer file.Close() // Closing the descriptor releases the OS lock.
	for {
		acquired, err := tryLock(file)
		if err != nil {
			return fmt.Errorf("lock JSON collection: %w", err)
		}
		if acquired {
			break
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	// A lock path must not have been removed or replaced while we waited.
	current, err := os.Lstat(lockPath)
	if err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(current, opened) {
		return errors.New("JSON collection lock path changed while locking")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

func openRegular(path string, create bool) (*os.File, error) {
	file, err := openNoFollow(path, create)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("JSON collection and lock paths must be regular files")
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func regularPath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil && !info.Mode().IsRegular() {
		return errors.New("JSON collection path must be a regular file, not a symlink or directory")
	}
	return err
}

func (c *Collection) read(ctx context.Context) ([]byte, bool, error) {
	file, err := openRegular(c.path, false)
	if errors.Is(err, os.ErrNotExist) {
		return []byte("[]"), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	data, err := io.ReadAll(contextReader{ctx, file})
	if err == nil {
		err = validate(data)
	}
	if err == nil {
		err = ctx.Err()
	}
	return data, true, err
}

func validate(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '[' || !json.Valid(trimmed) {
		return errors.New("JSON collection must contain a valid JSON array")
	}
	return nil
}

func (c *Collection) write(ctx context.Context, data []byte) error {
	if err := c.replace(ctx, data); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(c.path)); err != nil {
		return errors.Join(ErrWriteUncertain, err)
	}
	return nil
}

func (c *Collection) replace(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(c.path), "."+filepath.Base(c.path)+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if _, err := io.Copy(file, contextReader{ctx, bytes.NewReader(data)}); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := regularPath(c.path); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(file.Name(), c.path)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
