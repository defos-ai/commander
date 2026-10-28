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

const sourcesDocument = `schema_version: 1
agents:
  web:
    description: Fetch web pages
    source: github.com/defos-ai/web-agent@stable
    visibility: public
  builder-2:
    description: Build agents
    source: github.com/defos-ai/commander/apps/agents/builder@v0.1.0
    visibility: private
`

func TestRepositorySources(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "../../../..")
	if _, err := os.Stat(filepath.Join(root, "go.work")); errors.Is(err, os.ErrNotExist) {
		t.Skip("official sources is maintained in the Commander repository, outside the standalone framework module")
	}
	data, err := os.ReadFile(filepath.Join(root, "sources/agents.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := packages.ParseSources(data); err != nil {
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

func TestSourcesLookup(t *testing.T) {
	sources, err := packages.ParseSources([]byte(sourcesDocument))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := sources.Lookup("builder-2")
	want := packages.Entry{Description: "Build agents", Source: "github.com/defos-ai/commander/apps/agents/builder@v0.1.0", Visibility: packages.Private}
	if err != nil || entry != want {
		t.Fatalf("sources lookup: %#v, %v", entry, err)
	}
	public, err := sources.Lookup("web")
	if err != nil || public.Visibility != packages.Public {
		t.Fatalf("public sources lookup: %#v, %v", public, err)
	}
	for _, missing := range []string{"unknown", "Builder-2", " builder-2"} {
		if _, err := sources.Lookup(missing); err == nil {
			t.Fatalf("matched absent alias %q", missing)
		}
	}
	if _, err := packages.ParseSources([]byte("schema_version: 1\nagents: {}\n")); err != nil {
		t.Fatalf("explicit empty sources: %v", err)
	}
	if err := sources.Validate(); err != nil {
		t.Fatal(err)
	}
	entry.Source = "github.com/owner/repo@latest"
	sources.Agents["web"] = entry
	if err := sources.Validate(); err == nil {
		t.Fatal("accepted invalid package in a constructed sources")
	}
	if _, err := sources.Lookup("builder-2"); err == nil {
		t.Fatal("looked up an entry from an invalid sources")
	}
}

func TestSourcesRejectsInvalidDocuments(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"scalar", "hello"},
		{"sequence", "[hello]"},
		{"missing schema", strings.Replace(sourcesDocument, "schema_version: 1\n", "", 1)},
		{"unsupported schema", strings.Replace(sourcesDocument, "schema_version: 1", "schema_version: 2", 1)},
		{"quoted schema", strings.Replace(sourcesDocument, "schema_version: 1", "schema_version: \"1\"", 1)},
		{"float schema", strings.Replace(sourcesDocument, "schema_version: 1", "schema_version: 1.0", 1)},
		{"null schema", strings.Replace(sourcesDocument, "schema_version: 1", "schema_version: null", 1)},
		{"missing agents", "schema_version: 1\n"},
		{"null agents", "schema_version: 1\nagents: null\n"},
		{"sequence agents", "schema_version: 1\nagents: []\n"},
		{"scalar entry", "schema_version: 1\nagents: {web: hello}\n"},
		{"empty entry", "schema_version: 1\nagents: {web: {}}\n"},
		{"unknown root field", sourcesDocument + "command: echo hello\n"},
		{"unknown entry field", sourcesDocument + "    command: echo hello\n"},
		{"duplicate root field", sourcesDocument + "schema_version: 1\n"},
		{"duplicate alias", sourcesDocument + "  web: {}\n"},
		{"duplicate entry field", sourcesDocument + "    source: github.com/owner/repo@stable\n"},
		{"multiple documents", sourcesDocument + "---\n" + sourcesDocument},
		{"empty trailing document", sourcesDocument + "---\n"},
		{"anchor", strings.Replace(sourcesDocument, "agents:", "agents: &agents", 1)},
		{"alias", strings.Replace(sourcesDocument, "Fetch web pages", "*description", 1)},
		{"merge key", sourcesDocument + "    <<: {}\n"},
		{"custom tag", strings.Replace(sourcesDocument, "Fetch web pages", "!description Fetch web pages", 1)},
		{"nonstring alias", strings.Replace(sourcesDocument, "  web:", "  true:", 1)},
		{"uppercase alias", strings.Replace(sourcesDocument, "  web:", "  Web:", 1)},
		{"numeric alias prefix", strings.Replace(sourcesDocument, "  web:", "  1web:", 1)},
		{"underscore alias", strings.Replace(sourcesDocument, "  web:", "  web_agent:", 1)},
		{"empty description", strings.Replace(sourcesDocument, "Fetch web pages", "\"\"", 1)},
		{"untrimmed description", strings.Replace(sourcesDocument, "Fetch web pages", "\" Fetch web pages \"", 1)},
		{"numeric description", strings.Replace(sourcesDocument, "Fetch web pages", "123", 1)},
		{"boolean description", strings.Replace(sourcesDocument, "Fetch web pages", "true", 1)},
		{"null description", strings.Replace(sourcesDocument, "Fetch web pages", "null", 1)},
		{"numeric package", strings.Replace(sourcesDocument, "github.com/defos-ai/web-agent@stable", "123", 1)},
		{"invalid package", strings.Replace(sourcesDocument, "@stable", "@latest", 1)},
		{"missing visibility", strings.Replace(sourcesDocument, "    visibility: public\n", "", 1)},
		{"unknown visibility", strings.Replace(sourcesDocument, "visibility: public", "visibility: internal", 1)},
		{"boolean visibility", strings.Replace(sourcesDocument, "visibility: public", "visibility: true", 1)},
		{"null visibility", strings.Replace(sourcesDocument, "visibility: public", "visibility: null", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := packages.ParseSources([]byte(tc.data)); err == nil {
				t.Fatal("accepted invalid sources")
			}
		})
	}
}

func TestSourcesSizeLimit(t *testing.T) {
	data := sourcesDocument + "#" + strings.Repeat("a", packages.MaxSourcesBytes-len(sourcesDocument)-1)
	if _, err := packages.ParseSources([]byte(data)); err != nil {
		t.Fatalf("sources at size limit: %v", err)
	}
	if _, err := packages.ParseSources([]byte(data + "a")); err == nil {
		t.Fatal("accepted oversized sources")
	}
}
