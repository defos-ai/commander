package agent_test

import (
	"bytes"
	"slices"
	"testing"

	"github.com/defos-ai/commander/framework/agent/manifest"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func TestAgentManifestPublishesDependenciesWithoutChangingDeliverySelection(t *testing.T) {
	published := fixtures.DependencyManifest()

	data, err := manifest.Render(published)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manifest.Parse(data, published.Version)
	if err != nil {
		t.Fatalf("rendered dependency metadata could not be resolved: %v", err)
	}
	native, nativeErr := resolved.NativeFor("linux", "amd64")
	image, imageErr := resolved.DockerImage()

	if !slices.Equal(resolved.Dependencies, published.Dependencies) {
		t.Fatalf("publication changed dependency text, order or optional versions: %+v", resolved.Dependencies)
	}
	if nativeErr != nil || native != published.Native["linux/amd64"] || imageErr != nil || image != published.Docker.Image {
		t.Fatalf("dependencies changed delivery selection: native=%+v error=%v image=%s error=%v", native, nativeErr, image, imageErr)
	}
	resolved.Dependencies[0].Description = "changed after resolution"
	if published.Dependencies[0].Description == resolved.Dependencies[0].Description {
		t.Fatal("resolving metadata shared the publisher's dependency state")
	}
}

func TestAgentManifestAcceptsAbsentOrEmptyDependenciesAndRejectsMalformedDeclarations(t *testing.T) {
	published := fixtures.DependencyManifest()
	published.Dependencies = nil
	base, err := manifest.Render(published)
	if err != nil {
		t.Fatal(err)
	}
	for _, fields := range []string{"", "dependencies: []\n"} {
		data := append(bytes.Clone(base), fields...)

		resolved, err := manifest.Parse(data, published.Version)
		if err != nil || len(resolved.Dependencies) != 0 {
			t.Fatalf("empty dependency declarations changed resolution: %+v error=%v", resolved.Dependencies, err)
		}
		rendered, err := manifest.Render(resolved)
		if err != nil || bytes.Contains(rendered, []byte("dependencies:")) {
			t.Fatalf("empty dependencies were not omitted during publication: %s error=%v", rendered, err)
		}
	}
	for _, test := range fixtures.RejectedDependencyManifestFields() {
		t.Run(test.Name, func(t *testing.T) {
			data := append(bytes.Clone(base), test.YAML...)

			if _, err := manifest.Parse(data, published.Version); err == nil {
				t.Fatal("malformed manifest metadata was accepted")
			}
		})
	}
}
