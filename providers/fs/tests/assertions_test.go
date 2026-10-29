package providers_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/defos-ai/commander/framework/domain/executions"
)

func assertExecutionLogs(t *testing.T, page []executions.Log, err error, expected ...executions.Log) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if page == nil || len(page) != len(expected) {
		t.Fatalf("log page items=%d, want items=%d", len(page), len(expected))
	}
	for index, log := range page {
		if !log.Equal(expected[index]) {
			t.Fatalf("log at position %d changed: %+v, want %+v", index, log, expected[index])
		}
	}
}

func assertSecretFileUnchanged(t *testing.T, path string, expected []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, expected) {
		t.Fatalf("rejected operation changed persisted bytes: %v", err)
	}
}
