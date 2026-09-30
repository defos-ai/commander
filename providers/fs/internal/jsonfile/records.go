package jsonfile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Records publishes immutable JSON records through one atomic manifest. Each
// manifest has its own writer lock and blob directory; readers take no lock.
type Records struct {
	manifest  *Collection
	directory string
}

type recordRef struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type recordManifest struct {
	Version uint64      `json:"version"`
	Records []recordRef `json:"records"`
}

// The provider must prepare the manifest's parent directory durably before use.
// Records owns only its manifest, lock, and sibling blob directory.
func OpenRecords(ctx context.Context, path string) (*Records, error) {
	if _, err := filepath.EvalSymlinks(filepath.Dir(path)); err != nil {
		return nil, err
	}
	manifest, err := OpenContext(ctx, path)
	if err != nil {
		return nil, err
	}
	return &Records{manifest: manifest, directory: manifest.path + ".records"}, nil
}

func (r *Records) Read(ctx context.Context) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	err := r.View(ctx, func(view *RecordView) error {
		next := make(map[string]json.RawMessage, view.Len())
		for _, ref := range view.refs {
			data, err := view.Get(ref.Name)
			if err != nil {
				return err
			}
			next[ref.Name] = data
		}
		result = next
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, err
}

// View pins one manifest without a writer lock and reads bodies on demand. If
// collection removes an old blob, the whole callback may run again against a
// newer manifest. Keep results local to each attempt and avoid external effects.
func (r *Records) View(ctx context.Context, read func(*RecordView) error) error {
	if read == nil {
		return errors.New("record reader is required")
	}
	for {
		data, err := Read(ctx, r.manifest.path)
		if err != nil {
			return err
		}
		manifest, err := decodeRecords(data, true)
		if err != nil {
			return err
		}
		err = read(&RecordView{records: r, refs: manifest.Records, ctx: ctx})
		var missing *missingRecord
		if !errors.As(err, &missing) {
			if err != nil {
				return err
			}
			return ctx.Err()
		}
		// A subsequent writer may collect an older reader's blobs. Restart the
		// entire read from the new manifest rather than mixing two snapshots.
		latest, readErr := Read(ctx, r.manifest.path)
		if readErr != nil {
			return readErr
		}
		if bytes.Equal(data, latest) {
			return fmt.Errorf("committed record is missing: %v", err)
		}
	}
}

// Update publishes all changed records together. Callback and staging failures
// leave the manifest unchanged. Only a published manifest with an unconfirmed
// directory sync returns ErrWriteUncertain; its new snapshot remains readable.
func (r *Records) Update(ctx context.Context, mutate func(map[string]json.RawMessage) error) error {
	if mutate == nil {
		return errors.New("record mutation is required")
	}
	return r.manifest.locked(ctx, func() error {
		data, exists, err := r.manifest.read(ctx)
		if err != nil {
			return err
		}
		current, err := decodeRecords(data, exists)
		if err != nil {
			return err
		}
		records, err := r.readRecords(ctx, current.Records)
		if err != nil {
			return err
		}
		if err := mutate(records); err != nil {
			return err
		}
		next, err := references(records)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if exists && slices.Equal(current.Records, next) {
			// An identical retry must confirm a previous uncertain publication,
			// even though it does not replace any files or advance the version.
			if err := syncDirectory(filepath.Dir(r.manifest.path)); err != nil {
				return errors.Join(ErrWriteUncertain, err)
			}
			return nil
		}
		if current.Version == ^uint64(0) {
			return errors.New("record manifest version exhausted")
		}
		if err := r.prepareDirectory(); err != nil {
			return err
		}
		// Make the current manifest and blob directory durable before collecting
		// older generations, including after a previous uncertain publication.
		if err := syncDirectory(filepath.Dir(r.manifest.path)); err != nil {
			return err
		}
		if err := r.collect(ctx, current.Records); err != nil {
			return err
		}
		for _, ref := range next {
			if err := r.stage(ctx, ref, records[ref.Name]); err != nil {
				return err
			}
		}
		if err := syncDirectory(r.directory); err != nil {
			return err
		}
		manifest, err := json.Marshal([]recordManifest{{Version: current.Version + 1, Records: next}})
		if err != nil {
			return err
		}
		return r.manifest.write(ctx, append(manifest, '\n'))
	})
}

func (r *Records) prepareDirectory() error {
	if err := os.Mkdir(r.directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return r.checkDirectory()
}

func (r *Records) checkDirectory() error {
	info, err := os.Lstat(r.directory)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("record directory must not be a symlink or file")
	}
	return nil
}

func (r *Records) readRecords(ctx context.Context, refs []recordRef) (map[string]json.RawMessage, error) {
	result := make(map[string]json.RawMessage, len(refs))
	if len(refs) != 0 {
		if err := r.checkDirectory(); err != nil {
			return nil, err
		}
	}
	for _, ref := range refs {
		data, err := r.readRecord(ctx, ref.Digest)
		if err != nil {
			return nil, fmt.Errorf("read record %q: %w", ref.Name, err)
		}
		result[ref.Name] = data
	}
	return result, ctx.Err()
}

func (r *Records) readRecord(ctx context.Context, digest string) ([]byte, error) {
	file, err := openRegular(r.recordPath(digest), false)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(contextReader{ctx: ctx, reader: file})
	if err != nil {
		return nil, err
	}
	if recordDigest(data) != digest || !json.Valid(data) {
		return nil, errors.New("record content does not match its digest or is invalid JSON")
	}
	return data, nil
}

func (r *Records) stage(ctx context.Context, ref recordRef, data []byte) error {
	_, err := r.readRecord(ctx, ref.Digest)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Sync each file before publication, then sync the whole blob directory
	// once. Staging failures must never report a published manifest.
	return (&Collection{path: r.recordPath(ref.Digest)}).replace(ctx, data)
}

func (r *Records) collect(ctx context.Context, current []recordRef) error {
	keep := make(map[string]bool, len(current))
	for _, ref := range current {
		keep[ref.Digest+".json"] = true
	}
	entries, err := os.ReadDir(r.directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		blob := strings.HasSuffix(name, ".json") && validDigest(strings.TrimSuffix(name, ".json"))
		temporary := strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".tmp")
		if entry.IsDir() || keep[name] || !blob && !temporary {
			continue
		}
		if err := os.Remove(filepath.Join(r.directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (r *Records) recordPath(digest string) string {
	return filepath.Join(r.directory, digest+".json")
}

func references(records map[string]json.RawMessage) ([]recordRef, error) {
	refs := make([]recordRef, 0, len(records))
	for name, data := range records {
		if strings.TrimSpace(name) == "" || !json.Valid(data) {
			return nil, fmt.Errorf("record %q requires a name and valid JSON", name)
		}
		refs = append(refs, recordRef{Name: name, Digest: recordDigest(data)})
	}
	slices.SortFunc(refs, func(a, b recordRef) int { return strings.Compare(a.Name, b.Name) })
	return refs, nil
}

func decodeRecords(data []byte, exists bool) (recordManifest, error) {
	if !exists {
		return recordManifest{}, nil
	}
	var manifests []recordManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifests); err != nil {
		return recordManifest{}, fmt.Errorf("decode record manifest: %w", err)
	}
	if len(manifests) != 1 || manifests[0].Version == 0 || manifests[0].Records == nil {
		return recordManifest{}, errors.New("record manifest must contain one versioned record set")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return recordManifest{}, errors.New("record manifest must contain exactly one array")
	}
	manifest := manifests[0]
	for i, ref := range manifest.Records {
		if strings.TrimSpace(ref.Name) == "" || !validDigest(ref.Digest) || i > 0 && manifest.Records[i-1].Name >= ref.Name {
			return recordManifest{}, errors.New("record manifest must have sorted unique names and SHA-256 digests")
		}
	}
	return manifest, nil
}

func recordDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func validDigest(digest string) bool {
	return len(digest) == sha256.Size*2 && strings.Trim(digest, "0123456789abcdef") == ""
}
