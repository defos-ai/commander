// The sibling .lock file is persistent; its OS lock is released even on crash.
package jsonfile

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Collection struct {
	path string
	root *os.Root
}

var ErrWriteUncertain = errors.New("JSON collection was replaced but directory sync failed")

func New(path string) (*Collection, error) {
	return NewContext(context.Background(), path)
}

func NewContext(ctx context.Context, path string) (*Collection, error) {
	c, err := OpenContext(ctx, path)
	if err != nil {
		return nil, err
	}
	err = c.locked(ctx, func(c *Collection) error {
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
	// Callers build paths from configured roots and fixed, validated or encoded
	// components; Abs does not sanitize input. Root ancestors must be trusted.
	if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	// Resolve directory aliases so independent instances use the same lock.
	directory, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	c := &Collection{path: filepath.Join(directory, filepath.Base(absolute))}
	scoped, err := c.open()
	if err != nil {
		return nil, err
	}
	defer scoped.root.Close()
	if err := regularPath(scoped.root, filepath.Base(c.path)); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Collection) Path() string { return c.path }

// Read returns an atomic array snapshot without locking or creating any paths.
// It rejects symlinks and reports os.ErrNotExist for a missing collection.
func Read(ctx context.Context, path string) ([]byte, error) {
	return readSnapshot(ctx, path, false)
}

// ReadDurable confirms directory publication before exposing an atomic snapshot.
// It does not create paths or take a writer lock.
func ReadDurable(ctx context.Context, path string) ([]byte, error) {
	return readSnapshot(ctx, path, true)
}

func readSnapshot(ctx context.Context, path string, confirm bool) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("JSON collection requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := (&Collection{path: path}).open()
	if err != nil {
		return nil, err
	}
	defer c.root.Close()
	data, exists, err := c.read(ctx)
	if err == nil && !exists {
		err = &os.PathError{Op: "read", Path: path, Err: os.ErrNotExist}
	}
	if err == nil && confirm {
		err = syncRoot(c.root)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return data, err
}

// View supplies an independent snapshot under the collection lock. A missing
// file is an empty collection, including if it was removed after construction.
// Callbacks must not reenter this collection or wait for another writer to it.
func (c *Collection) View(ctx context.Context, fn func([]byte) error) error {
	return c.locked(ctx, func(c *Collection) error {
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
	return c.locked(ctx, func(c *Collection) error {
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

func (c *Collection) open() (*Collection, error) {
	directory := filepath.Dir(c.path)
	// Callers validate parent-path components. The identity check below detects
	// replacement during open; os.Root pins the directory for this operation.
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("JSON collection directory must not be a symlink or file")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = errors.New("JSON collection directory changed while opening")
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	return &Collection{path: c.path, root: root}, nil
}

func (c *Collection) locked(ctx context.Context, fn func(*Collection) error) error {
	if ctx == nil {
		return errors.New("JSON collection requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	scoped, err := c.open()
	if err != nil {
		return err
	}
	defer scoped.root.Close()
	lockPath := filepath.Base(c.path) + ".lock"
	file, err := openRegular(scoped.root, lockPath, true)
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
	current, err := scoped.root.Lstat(lockPath)
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
	return fn(scoped)
}

func openRegular(root *os.Root, name string, create bool) (*os.File, error) {
	filename := filepath.Base(name)
	if !filepath.IsLocal(name) || filename != name {
		return nil, errors.New("JSON collection file name must stay within its directory")
	}
	if err := regularPath(root, filename); err != nil {
		return nil, err
	}
	file, err := openRootFile(root, filename, create)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil {
		err = regularPath(root, filename)
	}
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("JSON collection and lock paths must be regular files")
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func regularPath(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil && !info.Mode().IsRegular() {
		return errors.New("JSON collection path must be a regular file, not a symlink or directory")
	}
	return err
}

func (c *Collection) read(ctx context.Context) ([]byte, bool, error) {
	file, err := openRegular(c.root, filepath.Base(c.path), false)
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
	if err := syncRoot(c.root); err != nil {
		return errors.Join(ErrWriteUncertain, err)
	}
	return nil
}

func (c *Collection) replace(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	temporary := "." + filepath.Base(c.path) + "-" + rand.Text() + ".tmp"
	file, err := c.root.OpenFile(temporary, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer c.root.Remove(temporary)
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
	if err := regularPath(c.root, filepath.Base(c.path)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.root.Rename(temporary, filepath.Base(c.path))
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
