package packages_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
)

func TestRuntimeManagersRejectUnapprovedSourcesBeforeSideEffects(t *testing.T) {
	for _, kind := range []runtimes.RuntimeKind{runtimes.RuntimeNative, runtimes.RuntimeDocker, runtimes.RuntimeKubernetes} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := fixtures.NewSourceAdmissionRuntime(t, kind)
			ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
			ctx = packages.WithGitHubToken(ctx, []byte("private-token-sentinel"))

			result, err := fixture.Manager.Spawn(ctx, requestID, fixtures.UntrustedSource)

			if !errors.Is(err, packages.ErrSourceApprovalRequired) || result.AgentID != "" {
				t.Fatalf("unapproved source did not return an approval requirement: %v", err)
			}
			if strings.Contains(err.Error(), "private-token-sentinel") || fixture.Source.RequestCount() != 0 || len(journal.Checkpoints()) != 0 {
				t.Fatal("rejected source exposed credentials, fetched metadata or saved execution state")
			}
			if fixture.NativeStorage != "" && len(fixtures.ChildReports(t, fixture.NativeStorage)) != 0 {
				t.Fatal("rejected source started a native child")
			}
			if fixture.Engine != nil {
				creates, starts, _ := fixture.Engine.Counts()
				if fixture.Engine.PullCount() != 0 || creates != 0 || starts != 0 {
					t.Fatal("rejected source pulled or launched a Docker image")
				}
			}
			if fixture.Pods != nil {
				creates, releases, gets, _ := fixture.Pods.Counts()
				if creates != 0 || releases != 0 || gets != 0 {
					t.Fatal("rejected source accessed or launched a Kubernetes Pod")
				}
			}
		})
	}
}

func TestPinPackageRejectsUnapprovedOrInvalidSourcesBeforeSideEffects(t *testing.T) {
	for _, test := range fixtures.SourceAdmissionSelectors() {
		t.Run(test.Name, func(t *testing.T) {
			resolver, source := fixtures.SourceAdmissionResolver(t)
			ctx, requestID, journal := fixtures.SpawnCheckpoints(t)

			pinned, err := runtimes.PinPackage(ctx, requestID, test.Reference, resolver)

			if err == nil || pinned != "" || errors.Is(err, packages.ErrSourceApprovalRequired) != test.RequiresApproval {
				t.Fatalf("source rejection lost its approval/validation distinction: %v", err)
			}
			if strings.Contains(err.Error(), "private-token-sentinel") || source.RequestCount() != 0 || len(journal.Checkpoints()) != 0 {
				t.Fatal("rejected package selection exposed credentials, resolved a release or saved state")
			}
		})
	}
}
