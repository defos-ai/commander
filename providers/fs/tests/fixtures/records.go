package fixtures

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

func OpenRecords(t *testing.T, path string) *jsonfile.Records {
	t.Helper()
	records, err := jsonfile.OpenRecords(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func PutRecords(t *testing.T, records *jsonfile.Records, values map[string]json.RawMessage) {
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

func RecordBlob(path, value string) string {
	return filepath.Join(path+".records", fmt.Sprintf("%x.json", sha256.Sum256([]byte(value))))
}
