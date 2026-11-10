package fixtures

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/local"
	"github.com/defos-ai/commander/apps/playground/tests/fakes"
)

func StartInstanceProbe(t *testing.T, probe *fakes.InstanceProbe) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(probe)
	t.Cleanup(server.Close)
	return server
}

func WriteLocalInstance(t *testing.T, directory string, instance local.Instance) {
	t.Helper()
	data, err := json.Marshal(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "instance.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
