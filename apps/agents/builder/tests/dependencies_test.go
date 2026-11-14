package builder_test

import (
	"slices"
	"strings"
	"testing"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func TestBuilderDependenciesGuidePartialToolInstallationAndRediscovery(t *testing.T) {
	instance := fixtures.DiscoveryBuilder(t)
	declared := instance.Describe()
	gitScope := fixtures.GitScope(t, fakes.GitConfig{})

	if err := instance.DiscoverActions(t.Context(), &environment.DiscoveryScope{Paths: []string{}}); err != nil {
		t.Fatal(err)
	}
	missing := instance.Describe()

	wantDependencies := map[string]string{"git": "", "go": "1.26.7+", "codex": ""}
	if len(missing.Dependencies) != len(wantDependencies) {
		t.Fatalf("default builder omitted installation guidance: %+v", missing.Dependencies)
	}
	for _, dependency := range missing.Dependencies {
		version, found := wantDependencies[dependency.Name]
		if !found || strings.TrimSpace(dependency.Description) == "" || dependency.Version != version {
			t.Fatalf("default builder declared incorrect installation guidance: %+v", dependency)
		}
		delete(wantDependencies, dependency.Name)
	}
	if len(wantDependencies) != 0 {
		t.Fatalf("default builder omitted required tools: %+v", wantDependencies)
	}
	assertBuilderExecutableRequirements(t, missing, map[string][]string{
		builderactions.CheckoutTemplateName: {"git"},
		builderactions.BuildAgentName:       {"codex", "go"},
		builderactions.PublishAgentName:     {"git"},
		builderactions.ReleaseAgentName:     nil,
	})
	if action := builderAction(t, missing, builderactions.CheckoutTemplateName); action.Status != actions.Unavailable {
		t.Fatalf("checkout was ready without Git: %+v", action)
	}

	if err := instance.DiscoverActions(t.Context(), gitScope); err != nil {
		t.Fatal(err)
	}
	partial := instance.Describe()

	assertBuilderExecutableRequirements(t, partial, map[string][]string{
		builderactions.CheckoutTemplateName: nil,
		builderactions.BuildAgentName:       {"codex", "go"},
		builderactions.PublishAgentName:     nil,
		builderactions.ReleaseAgentName:     nil,
	})
	if checkout := builderAction(t, partial, builderactions.CheckoutTemplateName); checkout.Status != actions.Ready || len(checkout.MissingRequirements) != 0 {
		t.Fatalf("installing Git did not make checkout ready: %+v", checkout)
	}
	build := builderAction(t, partial, builderactions.BuildAgentName)
	if build.Status != actions.Unavailable || !slices.Contains(build.MissingRequirements, actions.Requirement{Kind: actions.SecretRequirement, Name: "OPENAI_API_KEY"}) {
		t.Fatalf("partial installation lost build's independent tool/secret blockers: %+v", build)
	}
	if publish := builderAction(t, partial, builderactions.PublishAgentName); publish.Status != actions.Unavailable ||
		!slices.Contains(publish.MissingRequirements, actions.Requirement{Kind: actions.SecretRequirement, Name: "GITHUB_TOKEN"}) {
		t.Fatalf("Git installation incorrectly supplied a publication credential: %+v", publish)
	}
	if !slices.Equal(declared.Dependencies, missing.Dependencies) || !slices.Equal(missing.Dependencies, partial.Dependencies) {
		t.Fatal("discovery changed declared installation guidance")
	}
}

func assertBuilderExecutableRequirements(t *testing.T, snapshot agents.AgentSnapshot, want map[string][]string) {
	t.Helper()
	for name, expected := range want {
		var executableNames []string
		for _, requirement := range builderAction(t, snapshot, name).MissingRequirements {
			if requirement.Kind != actions.ExecutableRequirement {
				continue
			}
			dependency, found := snapshot.DependencyFor(requirement)
			if !found || dependency.Name != requirement.Name || strings.TrimSpace(dependency.Description) == "" {
				t.Fatalf("%s missing executable has no declared installation guidance: %+v", name, requirement)
			}
			executableNames = append(executableNames, requirement.Name)
		}
		slices.Sort(executableNames)
		if !slices.Equal(executableNames, expected) {
			t.Errorf("%s missing executables = %v, want %v", name, executableNames, expected)
		}
	}
}

func builderAction(t *testing.T, snapshot agents.AgentSnapshot, name string) actions.Action {
	t.Helper()
	for _, action := range snapshot.Actions {
		if action.Name == name {
			return action
		}
	}
	t.Fatalf("builder action %s is missing", name)
	return actions.Action{}
}
