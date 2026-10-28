package mongo_test

import (
	"testing"

	"github.com/defos-ai/commander/framework/testing/sessiontest"
	"github.com/defos-ai/commander/providers/mongo/sessions"
	"github.com/defos-ai/commander/providers/mongo/tests/fixtures"
)

func TestSessionQueries(t *testing.T) {
	store, err := sessions.NewSessionMongoStore(t.Context(), fixtures.OpenStore(t, "runtime_sessions"))
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.CheckQueries(t, store)
}
