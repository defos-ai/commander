package mongo_test

import (
	"testing"

	"github.com/defos-ai/commander/framework/testing/sessiontest"
	"github.com/defos-ai/commander/providers/mongo/sessions"
)

func TestSessionQueries(t *testing.T) {
	store, err := sessions.NewSessionMongoStore(t.Context(), openStore(t, "runtime_sessions"))
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.CheckQueries(t, store)
}
