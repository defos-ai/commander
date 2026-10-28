package fixtures

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/native"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
)

const UntrustedSource = "github.com/other/worker-agent@stable"

type SourceAdmissionSelector struct {
	Name, Reference  string
	RequiresApproval bool
}

func SourceAdmissionSelectors() []SourceAdmissionSelector {
	return []SourceAdmissionSelector{
		{Name: "foreign exact release", Reference: "github.com/other/worker-agent@v0.1.0", RequiresApproval: true},
		{Name: "foreign repository URL", Reference: "https://github.com/other/worker-agent", RequiresApproval: true},
		{Name: "owner prefix spoof", Reference: "github.com/defos-ai-attacker/worker-agent@stable", RequiresApproval: true},
		{Name: "owner suffix spoof", Reference: "github.com/other-defos-ai/worker-agent@stable", RequiresApproval: true},
		{Name: "owner in module path", Reference: "github.com/other/worker-agent/defos-ai@stable", RequiresApproval: true},
		{Name: "invalid version", Reference: "github.com/defos-ai/worker-agent@invalid"},
		{Name: "spoofed hostname", Reference: "https://github.com.attacker.example/defos-ai/worker-agent"},
		{Name: "URL credentials", Reference: "https://private-token-sentinel@github.com/defos-ai/worker-agent"},
	}
}

func SourceAdmissionResolver(t *testing.T) (*packages.Resolver, *fakes.PackageSource) {
	t.Helper()
	source := fakes.NewPackageSource(nil, FixtureReleaseURL, DockerImage)
	resolver := packages.NewResolver(source)
	t.Cleanup(resolver.CloseIdleConnections)
	return resolver, source
}

type SourceAdmissionRuntime struct {
	Manager       runtimes.Runtime
	Source        *fakes.PackageSource
	Engine        *fakes.DockerEngine
	Pods          *fakes.PodClient
	NativeStorage string
}

func NewSourceAdmissionRuntime(t *testing.T, kind runtimes.RuntimeKind) SourceAdmissionRuntime {
	t.Helper()
	resolver, source := SourceAdmissionResolver(t)
	fixture := SourceAdmissionRuntime{Source: source}
	switch kind {
	case runtimes.RuntimeNative:
		current := NewNativeManager(t, "", time.Second)
		if err := current.Manager.Close(); err != nil {
			t.Fatal(err)
		}
		current.Config.Resolver = resolver
		manager, err := native.NewManager(t.Context(), current.Config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := manager.Close(); err != nil {
				t.Error(err)
			}
		})
		fixture.Manager, fixture.NativeStorage = manager, current.Storage
	case runtimes.RuntimeDocker:
		current := NewDockerManager(t)
		current.Config.Resolver = resolver
		fixture.Manager, fixture.Engine = RestartDockerManager(t, current), current.Engine
	case runtimes.RuntimeKubernetes:
		current := NewKubernetesManager(t)
		current.Config.Resolver = resolver
		fixture.Manager, fixture.Pods = RestartKubernetesManager(t, current), current.Client
	default:
		t.Fatalf("unsupported source admission fixture runtime %q", kind)
	}
	return fixture
}
