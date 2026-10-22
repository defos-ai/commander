package packages_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/agent/packages"
)

const catalogSource = `schema_version: 1
agents:
  web:
    description: Fetch web pages
    package: github.com/defos-ai/web-agent@stable
    visibility: public
  builder-2:
    description: Build agents
    package: github.com/defos-ai/commander/apps/agents/builder@v0.1.0
    visibility: private
`

func TestRepositoryCatalog(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "../../../..")
	if _, err := os.Stat(filepath.Join(root, "go.work")); errors.Is(err, os.ErrNotExist) {
		t.Skip("official catalog is maintained in the Commander repository, outside the standalone framework module")
	}
	data, err := os.ReadFile(filepath.Join(root, "catalog/agents.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := packages.ParseCatalog(data); err != nil {
		t.Fatal(err)
	}
}

func TestPackageSelectors(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  packages.Reference
		tag   string
	}{
		{"github.com/defos-ai/web-agent@v0.1.0", packages.Reference{Owner: "defos-ai", Repository: "web-agent", Version: "v0.1.0"}, "v0.1.0"},
		{"github.com/defos-ai/commander/apps/agents/builder@v1.2.3", packages.Reference{Owner: "defos-ai", Repository: "commander", ModulePath: "apps/agents/builder", Version: "v1.2.3"}, "apps/agents/builder/v1.2.3"},
		{"github.com/Example/Repo.Name/Module_v2@v0.0.1", packages.Reference{Owner: "Example", Repository: "Repo.Name", ModulePath: "Module_v2", Version: "v0.0.1"}, "Module_v2/v0.0.1"},
		{"github.com/owner/repo/actions@v1.2.3", packages.Reference{Owner: "owner", Repository: "repo", ModulePath: "actions", Version: "v1.2.3"}, "actions/v1.2.3"},
		{"github.com/defos-ai/web-agent@stable", packages.Reference{Owner: "defos-ai", Repository: "web-agent", Version: "stable"}, "stable"},
		{"github.com/defos-ai/commander/apps/agents/builder@stable", packages.Reference{Owner: "defos-ai", Repository: "commander", ModulePath: "apps/agents/builder", Version: "stable"}, "apps/agents/builder/stable"},
		{"github.com/defos-ai/web-agent", packages.Reference{Owner: "defos-ai", Repository: "web-agent", Version: "stable"}, "stable"},
		{"https://github.com/defos-ai/web-agent/", packages.Reference{Owner: "defos-ai", Repository: "web-agent", Version: "stable"}, "stable"},
		{"https://github.com/defos-ai/web-agent@stable", packages.Reference{Owner: "defos-ai", Repository: "web-agent", Version: "stable"}, "stable"},
		{"https://github.com/defos-ai/commander/apps/agents/builder", packages.Reference{Owner: "defos-ai", Repository: "commander", ModulePath: "apps/agents/builder", Version: "stable"}, "apps/agents/builder/stable"},
		{"https://github.com/defos-ai/commander/apps/agents/builder@v1.2.3", packages.Reference{Owner: "defos-ai", Repository: "commander", ModulePath: "apps/agents/builder", Version: "v1.2.3"}, "apps/agents/builder/v1.2.3"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := packages.ParseSelector(tc.value)
			if err != nil || got != tc.want || got.String() != tc.want.String() || got.ReleaseTag() != tc.tag {
				t.Fatalf("selector: %#v, tag %q, error %v", got, got.ReleaseTag(), err)
			}
			pinned, err := packages.ParseReference(tc.value)
			if got.Version == "stable" || tc.value != got.String() {
				if err == nil {
					t.Fatal("exact package parser accepted a moving selector")
				}
			} else if err != nil || pinned != got {
				t.Fatalf("exact package parser changed reference: %#v, %v", pinned, err)
			}
		})
	}

	for _, value := range []string{
		"", "web", "github.com/owner/repo@", "github.com/owner/repo@latest",
		"github.com/owner/repo@v0.0.0", "github.com/owner/repo@v01.2.3", "github.com/owner/repo@v1.2",
		"github.com/owner/repo@v1.2.3-rc.1", "github.com/owner/repo@v1.2.3+build.1", "github.com/owner/repo@1.2.3",
		"github.com/owner/repo@Stable", "github.com/owner/repo@stable?token=secret", "github.com/owner/repo@v1.2.3#tag",
		"gitlab.com/owner/repo@v1.2.3", "github.com/owner/repo@v1.2.3@stable",
		" github.com/owner/repo@stable", "github.com/owner/repo@stable\n", "github.com//repo@stable",
		"github.com/owner//module@stable", "github.com/owner/repo/@stable", "github.com/owner/repo/module//nested@stable",
		"github.com/../example-agent@v1.2.3", "github.com/defos-ai/../example-agent@v1.2.3",
		"github.com/owner/repo/../module@stable", "github.com/owner/repo/module..name@stable", "github.com/owner/repo/module.@stable",
		"github.com/owner/repo/module.lock@stable", "github.com/owner/repo/module%2fnested@stable", `github.com/owner/repo/module\nested@stable`,
		"http://github.com/owner/repo", "https://github.com:443/owner/repo", "https://github.com:8443/owner/repo",
		"https://user:secret@github.com/owner/repo", "https://github.com.evil/owner/repo", "https://evil.example/owner/repo",
		"https://github.com/owner/repo?token=secret", "https://github.com/owner/repo?", "https://github.com/owner/repo#",
		"https://github.com/owner/repo#fragment", "https://github.com/owner/repo/%61pps", "https://github.com/owner/repo/tree/main",
		"https://github.com/owner/repo\n@v1.2.3", "https://github.com/owner/\trepo",
		"https://github.com/owner/repo/blob/main/agent.yaml", "github.com/owner/repo/tree/main", "https://github.com/owner/repo/releases/tag/v1.2.3",
	} {
		t.Run("invalid/"+value, func(t *testing.T) {
			if _, err := packages.ParseSelector(value); err == nil {
				t.Fatal("accepted invalid package selector")
			}
			if _, err := packages.ParseReference(value); err == nil {
				t.Fatal("accepted invalid exact package reference")
			}
		})
	}
}

func TestCatalogLookup(t *testing.T) {
	catalog, err := packages.ParseCatalog([]byte(catalogSource))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := catalog.Lookup("builder-2")
	want := packages.Entry{Description: "Build agents", Package: "github.com/defos-ai/commander/apps/agents/builder@v0.1.0", Visibility: packages.Private}
	if err != nil || entry != want {
		t.Fatalf("catalog lookup: %#v, %v", entry, err)
	}
	public, err := catalog.Lookup("web")
	if err != nil || public.Visibility != packages.Public {
		t.Fatalf("public catalog lookup: %#v, %v", public, err)
	}
	for _, missing := range []string{"unknown", "Builder-2", " builder-2"} {
		if _, err := catalog.Lookup(missing); err == nil {
			t.Fatalf("matched absent alias %q", missing)
		}
	}
	if _, err := packages.ParseCatalog([]byte("schema_version: 1\nagents: {}\n")); err != nil {
		t.Fatalf("explicit empty catalog: %v", err)
	}
	if err := catalog.Validate(); err != nil {
		t.Fatal(err)
	}
	entry.Package = "github.com/owner/repo@latest"
	catalog.Agents["web"] = entry
	if err := catalog.Validate(); err == nil {
		t.Fatal("accepted invalid package in a constructed catalog")
	}
	if _, err := catalog.Lookup("builder-2"); err == nil {
		t.Fatal("looked up an entry from an invalid catalog")
	}
}

func TestCatalogRejectsInvalidDocuments(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"scalar", "hello"},
		{"sequence", "[hello]"},
		{"missing schema", strings.Replace(catalogSource, "schema_version: 1\n", "", 1)},
		{"unsupported schema", strings.Replace(catalogSource, "schema_version: 1", "schema_version: 2", 1)},
		{"quoted schema", strings.Replace(catalogSource, "schema_version: 1", "schema_version: \"1\"", 1)},
		{"float schema", strings.Replace(catalogSource, "schema_version: 1", "schema_version: 1.0", 1)},
		{"null schema", strings.Replace(catalogSource, "schema_version: 1", "schema_version: null", 1)},
		{"missing agents", "schema_version: 1\n"},
		{"null agents", "schema_version: 1\nagents: null\n"},
		{"sequence agents", "schema_version: 1\nagents: []\n"},
		{"scalar entry", "schema_version: 1\nagents: {web: hello}\n"},
		{"empty entry", "schema_version: 1\nagents: {web: {}}\n"},
		{"unknown root field", catalogSource + "command: echo hello\n"},
		{"unknown entry field", catalogSource + "    command: echo hello\n"},
		{"duplicate root field", catalogSource + "schema_version: 1\n"},
		{"duplicate alias", catalogSource + "  web: {}\n"},
		{"duplicate entry field", catalogSource + "    package: github.com/owner/repo@stable\n"},
		{"multiple documents", catalogSource + "---\n" + catalogSource},
		{"empty trailing document", catalogSource + "---\n"},
		{"anchor", strings.Replace(catalogSource, "agents:", "agents: &agents", 1)},
		{"alias", strings.Replace(catalogSource, "Fetch web pages", "*description", 1)},
		{"merge key", catalogSource + "    <<: {}\n"},
		{"custom tag", strings.Replace(catalogSource, "Fetch web pages", "!description Fetch web pages", 1)},
		{"nonstring alias", strings.Replace(catalogSource, "  web:", "  true:", 1)},
		{"uppercase alias", strings.Replace(catalogSource, "  web:", "  Web:", 1)},
		{"numeric alias prefix", strings.Replace(catalogSource, "  web:", "  1web:", 1)},
		{"underscore alias", strings.Replace(catalogSource, "  web:", "  web_agent:", 1)},
		{"empty description", strings.Replace(catalogSource, "Fetch web pages", "\"\"", 1)},
		{"untrimmed description", strings.Replace(catalogSource, "Fetch web pages", "\" Fetch web pages \"", 1)},
		{"numeric description", strings.Replace(catalogSource, "Fetch web pages", "123", 1)},
		{"boolean description", strings.Replace(catalogSource, "Fetch web pages", "true", 1)},
		{"null description", strings.Replace(catalogSource, "Fetch web pages", "null", 1)},
		{"numeric package", strings.Replace(catalogSource, "github.com/defos-ai/web-agent@stable", "123", 1)},
		{"invalid package", strings.Replace(catalogSource, "@stable", "@latest", 1)},
		{"missing visibility", strings.Replace(catalogSource, "    visibility: public\n", "", 1)},
		{"unknown visibility", strings.Replace(catalogSource, "visibility: public", "visibility: internal", 1)},
		{"boolean visibility", strings.Replace(catalogSource, "visibility: public", "visibility: true", 1)},
		{"null visibility", strings.Replace(catalogSource, "visibility: public", "visibility: null", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := packages.ParseCatalog([]byte(tc.data)); err == nil {
				t.Fatal("accepted invalid catalog")
			}
		})
	}
}

func TestCatalogSizeLimit(t *testing.T) {
	data := catalogSource + "#" + strings.Repeat("a", packages.MaxCatalogBytes-len(catalogSource)-1)
	if _, err := packages.ParseCatalog([]byte(data)); err != nil {
		t.Fatalf("catalog at size limit: %v", err)
	}
	if _, err := packages.ParseCatalog([]byte(data + "a")); err == nil {
		t.Fatal("accepted oversized catalog")
	}
}
