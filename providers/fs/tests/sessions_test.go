package providers_test

import (
	"testing"

	"github.com/defos-ai/commander/framework/testing/sessiontest"
	"github.com/defos-ai/commander/providers/fs/sessions"
)

func TestSessionQueries(t *testing.T) {
	store, err := sessions.NewSessionFsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.CheckQueries(t, store)
}
