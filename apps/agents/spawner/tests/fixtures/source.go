package fixtures

import (
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
)

type PackageSource struct {
	*fakes.PackageSource
	resolver *packages.Resolver
}

func NewPackageSource(t *testing.T) *PackageSource {
	t.Helper()
	source := fakes.NewPackageSource(NativeBinary(t), FixtureReleaseURL, DockerImage)
	resolver := packages.NewResolver(source)
	t.Cleanup(resolver.CloseIdleConnections)
	return &PackageSource{PackageSource: source, resolver: resolver}
}
func (source *PackageSource) Resolver() *packages.Resolver { return source.resolver }
