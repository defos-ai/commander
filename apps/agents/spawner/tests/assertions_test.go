package packages_test

import (
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"testing"
)

func assertKubernetesLaunchCounts(t *testing.T, client *fakes.PodClient, creates, releases int) {
	t.Helper()
	actualCreates, actualReleases, _, closes := client.Counts()
	if actualCreates != creates || actualReleases != releases || closes != 0 {
		t.Fatalf("unexpected Pod lifecycle: create=%d release=%d client close=%d; want %d/%d/0", actualCreates, actualReleases, closes, creates, releases)
	}
}
