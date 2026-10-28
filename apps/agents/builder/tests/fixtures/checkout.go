package fixtures

import (
	"testing"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
)

type Checkout struct {
	*builderactions.CheckoutTemplate
	Scope *environment.DiscoveryScope
}

func LocalCheckout(t *testing.T, root, source string) *Checkout {
	t.Helper()
	return ConfiguredCheckout(t, root, fakes.GitConfig{Source: source})
}

func ConfiguredCheckout(t *testing.T, root string, config fakes.GitConfig) *Checkout {
	t.Helper()
	executor, err := builderactions.NewCheckoutTemplate(root)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &Checkout{CheckoutTemplate: executor, Scope: GitScope(t, config)}
	fixture.DiscoverReady(t)
	return fixture
}

func (f *Checkout) DiscoverReady(t *testing.T) {
	t.Helper()
	status, err := f.Discover(t.Context(), &environment.Local{}, f.Scope)
	if err != nil || status != action.Ready {
		t.Fatalf("Git discovery: status=%s error=%v", status, err)
	}
}
