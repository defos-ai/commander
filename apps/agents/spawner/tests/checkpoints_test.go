package packages_test

import (
	"bytes"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
)

func assertCheckpointExcludes(t *testing.T, journal *fakes.Checkpoints, values ...string) {
	t.Helper()
	facts := journal.Checkpoints()
	if len(facts) == 0 {
		t.Fatal("missing execution checkpoints")
	}
	for _, data := range facts {
		for _, value := range values {
			if bytes.Contains(data, []byte(value)) {
				t.Fatal("execution checkpoint contains a bootstrap value")
			}
		}
	}
}
