package manifest_test

import (
	"bytes"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/agent/manifest"
)

const manifestHeader = "schema_version: 1\ntype: example-agent\nversion: v0.1.0\n"

var (
	manifestChecksum = strings.Repeat("a", 64)
	manifestNative   = "native:\n  linux/amd64:\n    asset: commander-example-agent_linux_amd64\n    sha256: \"" + manifestChecksum + "\"\n"
	manifestImage    = "ghcr.io/defos-ai/commander-example-agent@sha256:" + strings.Repeat("b", 64)
	manifestDocker   = "docker:\n  image: " + manifestImage + "\n"
)

func TestManifestDeliveryModes(t *testing.T) {
	for _, mode := range []struct {
		name   string
		body   string
		native bool
		docker bool
	}{
		{"native", manifestNative, true, false},
		{"docker", manifestDocker, false, true},
		{"both", manifestNative + manifestDocker, true, true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			parsed, err := manifest.Parse([]byte(manifestHeader+mode.body), "v0.1.0")
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Type != "example-agent" || parsed.Version != "v0.1.0" {
				t.Fatalf("unexpected package identity: %#v", parsed)
			}
			asset, err := parsed.NativeFor("linux", "amd64")
			if mode.native {
				if err != nil || asset.Asset != "commander-example-agent_linux_amd64" || asset.SHA256 != manifestChecksum {
					t.Fatalf("native selection: %#v, %v", asset, err)
				}
			} else if err == nil {
				t.Fatal("selected unavailable native mode")
			}
			image, err := parsed.DockerImage()
			if mode.docker {
				if err != nil || image != manifestImage {
					t.Fatalf("Docker selection: %q, %v", image, err)
				}
			} else if err == nil {
				t.Fatal("selected unavailable Docker mode")
			}
			rendered, err := manifest.Render(parsed)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip, err := manifest.Parse(rendered, parsed.Version)
			if err != nil || !reflect.DeepEqual(parsed, roundTrip) {
				t.Fatalf("rendered manifest did not round trip: %#v, %v", roundTrip, err)
			}
			if _, err := parsed.NativeFor("darwin", "arm64"); err == nil {
				t.Fatal("selected unavailable native platform")
			}
		})
	}
}

func TestManifestRenderNativeAssets(t *testing.T) {
	for _, asset := range []string{"null", "123", "true", "1e3"} {
		t.Run(asset, func(t *testing.T) {
			input := manifest.Manifest{
				SchemaVersion: 1,
				Type:          "example-agent",
				Version:       "v0.1.0",
				Native: map[string]manifest.NativeAsset{
					"linux/amd64":  {Asset: asset, SHA256: manifestChecksum},
					"darwin/arm64": {Asset: "darwin-agent", SHA256: manifestChecksum},
				},
			}
			rendered, err := manifest.Render(input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := manifest.Parse(rendered, input.Version)
			if err != nil || !reflect.DeepEqual(parsed, input) {
				t.Fatalf("YAML-sensitive asset did not round trip: %#v, %v", parsed, err)
			}
			if bytes.Index(rendered, []byte("darwin/arm64")) > bytes.Index(rendered, []byte("linux/amd64")) {
				t.Fatal("native platforms are not rendered in sorted order")
			}
			input.Native = map[string]manifest.NativeAsset{
				"darwin/arm64": input.Native["darwin/arm64"],
				"linux/amd64":  input.Native["linux/amd64"],
			}
			reordered, err := manifest.Render(input)
			if err != nil || !bytes.Equal(rendered, reordered) {
				t.Fatalf("map insertion order changed rendered bytes: %v", err)
			}
		})
	}
}

func TestManifestCapabilityDescriptionsRoundTrip(t *testing.T) {
	input := manifest.Manifest{
		SchemaVersion: 1, Type: "example-agent", Version: "v0.1.0",
		Responsibilities: []string{"Review a page: HTML, links and text.", "123", "true", "null", "Write a report.\nInclude findings."},
		Actions: []manifest.Action{
			{Name: "write-summary", Description: "Write a Markdown report with \"quoted\" findings."},
			{Name: "analyze-text", Description: "Count words and lines.\nPreserve Unicode: café."},
		},
		Docker: &manifest.Docker{Image: manifestImage},
	}
	before := append([]manifest.Action(nil), input.Actions...)
	rendered, err := manifest.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input.Actions, before) {
		t.Fatal("rendering reordered the caller's action slice")
	}
	parsed, err := manifest.Parse(rendered, input.Version)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.Responsibilities, input.Responsibilities) || !reflect.DeepEqual(parsed.Actions, []manifest.Action{before[1], before[0]}) {
		t.Fatalf("description text/order did not round trip: %#v", parsed)
	}
	input.Actions[0], input.Actions[1] = input.Actions[1], input.Actions[0]
	reordered, err := manifest.Render(input)
	if err != nil || !bytes.Equal(reordered, rendered) {
		t.Fatalf("action declaration order changed rendered bytes: %v", err)
	}
	input.Responsibilities, input.Actions = nil, nil
	withoutDescriptions, err := manifest.Render(input)
	if err != nil || bytes.Contains(withoutDescriptions, []byte("responsibilities:")) || bytes.Contains(withoutDescriptions, []byte("actions:")) {
		t.Fatalf("omitted descriptions were rendered: %s, %v", withoutDescriptions, err)
	}
}

func TestManifestRenderRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*manifest.Manifest)
	}{
		{"unsupported schema", func(m *manifest.Manifest) { m.SchemaVersion = 2 }},
		{"invalid type", func(m *manifest.Manifest) { m.Type = "Example-agent" }},
		{"invalid version", func(m *manifest.Manifest) { m.Version = "latest" }},
		{"no delivery mode", func(m *manifest.Manifest) { m.Native, m.Docker = nil, nil }},
		{"empty native with Docker", func(m *manifest.Manifest) { m.Native = map[string]manifest.NativeAsset{} }},
		{"empty Docker with native", func(m *manifest.Manifest) { m.Docker = &manifest.Docker{} }},
		{"invalid platform", func(m *manifest.Manifest) { m.Native["../linux/amd64"] = m.Native["linux/amd64"] }},
		{"unsafe asset", func(m *manifest.Manifest) {
			m.Native["linux/amd64"] = manifest.NativeAsset{Asset: "../agent", SHA256: manifestChecksum}
		}},
		{"invalid checksum", func(m *manifest.Manifest) {
			m.Native["linux/amd64"] = manifest.NativeAsset{Asset: "agent", SHA256: "placeholder"}
		}},
		{"unpinned Docker with native", func(m *manifest.Manifest) { m.Docker.Image = "ghcr.io/defos-ai/example:latest" }},
		{"oversized output", func(m *manifest.Manifest) {
			m.Native["linux/amd64"] = manifest.NativeAsset{Asset: strings.Repeat("a", manifest.MaxBytes), SHA256: manifestChecksum}
		}},
		{"empty responsibilities", func(m *manifest.Manifest) { m.Responsibilities = []string{} }},
		{"too many responsibilities", func(m *manifest.Manifest) { m.Responsibilities = make([]string, 101) }},
		{"blank responsibility", func(m *manifest.Manifest) { m.Responsibilities = []string{" "} }},
		{"untrimmed responsibility", func(m *manifest.Manifest) { m.Responsibilities = []string{"Analyze text\n"} }},
		{"invalid UTF-8 responsibility", func(m *manifest.Manifest) { m.Responsibilities = []string{string([]byte{0xff})} }},
		{"oversized responsibility", func(m *manifest.Manifest) { m.Responsibilities = []string{strings.Repeat("a", 8193)} }},
		{"oversized descriptions together", func(m *manifest.Manifest) {
			for range 8 {
				m.Responsibilities = append(m.Responsibilities, strings.Repeat("a", 8192))
			}
		}},
		{"empty actions", func(m *manifest.Manifest) { m.Actions = []manifest.Action{} }},
		{"too many actions", func(m *manifest.Manifest) { m.Actions = make([]manifest.Action, 101) }},
		{"duplicate actions", func(m *manifest.Manifest) {
			m.Actions = []manifest.Action{{Name: "analyze-text", Description: "Analyze text."}, {Name: "analyze-text", Description: "Another description."}}
		}},
		{"unbounded action name", func(m *manifest.Manifest) {
			m.Actions = []manifest.Action{{Name: strings.Repeat("a", 129), Description: "Analyze text."}}
		}},
		{"unsafe action name", func(m *manifest.Manifest) {
			m.Actions = []manifest.Action{{Name: "analyze text", Description: "Analyze text."}}
		}},
		{"empty action description", func(m *manifest.Manifest) {
			m.Actions = []manifest.Action{{Name: "analyze-text"}}
		}},
		{"untrimmed action description", func(m *manifest.Manifest) {
			m.Actions = []manifest.Action{{Name: "analyze-text", Description: " Analyze text."}}
		}},
		{"oversized action description", func(m *manifest.Manifest) {
			m.Actions = []manifest.Action{{Name: "analyze-text", Description: strings.Repeat("a", 8193)}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, err := manifest.Parse([]byte(manifestHeader+manifestNative+manifestDocker), "v0.1.0")
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(&input)
			if rendered, err := manifest.Render(input); err == nil || len(rendered) != 0 {
				t.Fatalf("invalid manifest produced %d bytes, error: %v", len(rendered), err)
			}
		})
	}
}

func TestManifestRejectsInvalidYAMLAndSchema(t *testing.T) {
	valid := manifestHeader + manifestNative
	for _, tc := range []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"scalar", "hello"},
		{"sequence", "[hello]"},
		{"root null", "null"},
		{"missing schema", strings.Replace(valid, "schema_version: 1\n", "", 1)},
		{"unsupported schema", strings.Replace(valid, "schema_version: 1", "schema_version: 2", 1)},
		{"quoted schema", strings.Replace(valid, "schema_version: 1", "schema_version: \"1\"", 1)},
		{"float schema", strings.Replace(valid, "schema_version: 1", "schema_version: 1.0", 1)},
		{"null schema", strings.Replace(valid, "schema_version: 1", "schema_version: null", 1)},
		{"root unknown key", valid + "command: echo hello\n"},
		{"nested unknown key", valid + "    command: echo hello\n"},
		{"Docker unknown key", manifestHeader + manifestDocker + "  privileged: yes\n"},
		{"duplicate root key", valid + "type: other-agent\n"},
		{"duplicate nested key", valid + "    asset: other\n"},
		{"duplicate platform", valid + strings.TrimPrefix(manifestNative, "native:\n")},
		{"multiple documents", valid + "---\n" + valid},
		{"empty trailing document", valid + "---\n"},
		{"anchor", strings.Replace(valid, "type: example-agent", "type: &kind example-agent", 1)},
		{"alias", strings.Replace(valid, "type: example-agent", "type: *kind", 1)},
		{"merge key", valid + "<<: {}\n"},
		{"custom tag", strings.Replace(valid, "type: example-agent", "type: !agent example-agent", 1)},
		{"null native", manifestHeader + "native: null\n" + manifestDocker},
		{"null Docker", valid + "docker: null\n"},
		{"null asset", strings.Replace(valid, "asset: commander-example-agent_linux_amd64", "asset: null", 1)},
		{"numeric type", strings.Replace(valid, "type: example-agent", "type: 123", 1)},
		{"numeric asset", strings.Replace(valid, "asset: commander-example-agent_linux_amd64", "asset: 123", 1)},
		{"numeric checksum", strings.Replace(valid, "\""+manifestChecksum+"\"", strings.Repeat("1", 64), 1)},
		{"nonstring key", valid + "true: unused\n"},
		{"no delivery mode", manifestHeader},
		{"empty native", manifestHeader + "native: {}\n"},
		{"empty native with Docker", manifestHeader + "native: {}\n" + manifestDocker},
		{"empty Docker with native", valid + "docker: {}\n"},
		{"missing asset", strings.Replace(valid, "    asset: commander-example-agent_linux_amd64\n", "", 1)},
		{"missing checksum", strings.Replace(valid, "    sha256: \""+manifestChecksum+"\"\n", "", 1)},
		{"sequence native", manifestHeader + "native: []\n"},
		{"scalar native", manifestHeader + "native: hello\n"},
		{"sequence entry", manifestHeader + "native:\n  linux/amd64: []\n"},
		{"null responsibilities", valid + "responsibilities: null\n"},
		{"empty responsibilities", valid + "responsibilities: []\n"},
		{"scalar responsibilities", valid + "responsibilities: Analyze text\n"},
		{"mapping responsibilities", valid + "responsibilities: {name: Analyze text}\n"},
		{"numeric responsibility", valid + "responsibilities: [123]\n"},
		{"boolean responsibility", valid + "responsibilities: [true]\n"},
		{"mapping responsibility entry", valid + "responsibilities: [{name: Analyze text}]\n"},
		{"nested responsibility list", valid + "responsibilities: [[Analyze text]]\n"},
		{"anchored responsibility list", valid + "responsibilities: &items [Analyze text]\n"},
		{"anchored responsibility", valid + "responsibilities: [&item Analyze text]\n"},
		{"aliased responsibility", valid + "responsibilities: [*item]\n"},
		{"custom responsibility list tag", valid + "responsibilities: !items [Analyze text]\n"},
		{"null actions", valid + "actions: null\n"},
		{"empty actions", valid + "actions: []\n"},
		{"mapping actions", valid + "actions: {name: analyze-text, description: Analyze text}\n"},
		{"scalar action entry", valid + "actions: [analyze-text]\n"},
		{"numeric action name", valid + "actions: [{name: 123, description: Analyze text}]\n"},
		{"numeric action description", valid + "actions: [{name: analyze-text, description: 123}]\n"},
		{"boolean action description", valid + "actions: [{name: analyze-text, description: true}]\n"},
		{"missing action name", valid + "actions: [{description: Analyze text}]\n"},
		{"missing action description", valid + "actions: [{name: analyze-text}]\n"},
		{"unknown action field", valid + "actions: [{name: analyze-text, description: Analyze text, status: ready}]\n"},
		{"duplicate action field", valid + "actions: [{name: analyze-text, name: other, description: Analyze text}]\n"},
		{"anchored action entry", valid + "actions: [&item {name: analyze-text, description: Analyze text}]\n"},
		{"nested action sequence", valid + "actions: [[{name: analyze-text, description: Analyze text}]]\n"},
		{"too many responsibilities", valid + "responsibilities:\n" + strings.Repeat("  - Analyze text\n", 101)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := manifest.Parse([]byte(tc.data), "v0.1.0"); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}

func TestManifestRejectsInvalidPackageIdentity(t *testing.T) {
	valid := manifestHeader + manifestNative
	for _, agentType := range []string{"", "Example-agent", "-agent", "agent-", "some--agent", "one/two", strings.Repeat("a", 49) + "-agent"} {
		t.Run("type/"+agentType, func(t *testing.T) {
			data := strings.Replace(valid, "type: example-agent", "type: \""+agentType+"\"", 1)
			if _, err := manifest.Parse([]byte(data), "v0.1.0"); err == nil {
				t.Fatal("accepted invalid agent type")
			}
		})
	}
	for _, version := range []string{"", "latest", "stable", "0.1.0", "v0.0.0", "v01.0.0", "v1.2", "v1.2.3-rc.1", "v1.2.3+build.1"} {
		t.Run("version/"+version, func(t *testing.T) {
			data := strings.Replace(valid, "version: v0.1.0", "version: \""+version+"\"", 1)
			if _, err := manifest.Parse([]byte(data), version); err == nil {
				t.Fatal("accepted invalid release version")
			}
		})
	}
	if _, err := manifest.Parse([]byte(valid), "v0.2.0"); err == nil {
		t.Fatal("accepted manifest from another release")
	}
	maxType := strings.Repeat("a", 48) + "-agent"
	data := strings.Replace(valid, "type: example-agent", "type: "+maxType, 1)
	if _, err := manifest.Parse([]byte(data), "v0.1.0"); err != nil {
		t.Fatalf("rejected type producing a 63-byte agent ID: %v", err)
	}
}

func TestManifestRejectsUnsafeNativeAssets(t *testing.T) {
	valid := manifestHeader + manifestNative
	for _, asset := range []string{"", ".", "..", ".hidden", "agent.", "agent..", "../agent", "/tmp/agent", `..\agent`, `C:\agent.exe`, "https://example.org/agent", "agent file", "agent?token=secret", "a%2fb", "agént", "agent\nfile"} {
		t.Run(asset, func(t *testing.T) {
			parsed, err := manifest.Parse([]byte(valid), "v0.1.0")
			if err != nil {
				t.Fatal(err)
			}
			parsed.Native["linux/amd64"] = manifest.NativeAsset{Asset: asset, SHA256: manifestChecksum}
			if err := parsed.Validate(); err == nil {
				t.Fatal("accepted unsafe asset filename")
			}
			if _, err := parsed.NativeFor("linux", "amd64"); err == nil {
				t.Fatal("selected unsafe asset from an unvalidated manifest")
			}
		})
	}
	for _, checksum := range []string{"", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		data := strings.Replace(valid, manifestChecksum, checksum, 1)
		if _, err := manifest.Parse([]byte(data), "v0.1.0"); err == nil {
			t.Errorf("accepted invalid checksum %q", checksum)
		}
	}
	for _, platform := range []string{"linux", "linux/amd64/extra", "Linux/amd64", "linux/../amd64"} {
		data := strings.Replace(valid, "linux/amd64:", platform+":", 1)
		if _, err := manifest.Parse([]byte(data), "v0.1.0"); err == nil {
			t.Errorf("accepted invalid platform %q", platform)
		}
	}
}

func TestManifestRejectsWindowsDeviceAssets(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("device filename restrictions apply on Windows")
	}
	for _, asset := range []string{"NUL", "nul", "CON", "AUX", "PRN", "COM1", "LPT1", "COM1.exe"} {
		t.Run(asset, func(t *testing.T) {
			// Recent Windows versions permit device names with extensions.
			if asset == "COM1.exe" && filepath.IsLocal(asset) {
				t.Skip("this Windows version permits COM1.exe as a regular filename")
			}
			data := strings.Replace(manifestHeader+manifestNative, "asset: commander-example-agent_linux_amd64", "asset: "+asset, 1)
			if _, err := manifest.Parse([]byte(data), "v0.1.0"); err == nil {
				t.Fatal("accepted a Windows device as an asset filename")
			}
		})
	}
}

func TestManifestRejectsUnpinnedDockerImages(t *testing.T) {
	digest := strings.Repeat("b", 64)
	for _, image := range []string{
		"", "ghcr.io/defos-ai/example:stable", "ghcr.io/defos-ai/example",
		"ghcr.io/defos-ai/example:v1@sha256:" + digest,
		"ghcr.io/defos-ai/example@sha256:" + strings.ToUpper(digest),
		"ghcr.io/defos-ai/example@sha256:" + strings.Repeat("g", 64),
		"ghcr.io/defos-ai/example@sha256:" + digest[:63],
		"ghcr.io/defos-ai/example@sha512:" + strings.Repeat("b", 128),
		"example@sha256:" + digest,
		"defos-ai/example@sha256:" + digest,
		"https://ghcr.io/defos-ai/example@sha256:" + digest,
		"ghcr.io/defos-ai/Example@sha256:" + digest,
		"user:password@ghcr.io/defos-ai/example@sha256:" + digest,
	} {
		t.Run(image, func(t *testing.T) {
			// An invalid supplied mode must fail even when native mode is usable.
			data := manifestHeader + manifestNative + "docker:\n  image: \"" + image + "\"\n"
			if _, err := manifest.Parse([]byte(data), "v0.1.0"); err == nil {
				t.Fatal("accepted unsafe Docker image reference")
			}
		})
	}
	for _, image := range []string{"ghcr.io/defos-ai/example@sha256:" + digest, "registry.example.org:5000/team/example@sha256:" + digest, "docker.io/library/example@sha256:" + digest} {
		data := manifestHeader + "docker:\n  image: " + image + "\n"
		parsed, err := manifest.Parse([]byte(data), "v0.1.0")
		if err != nil {
			t.Fatalf("rejected pinned image %q: %v", image, err)
		}
		parsed.Docker.Image = "ghcr.io/defos-ai/example:latest"
		if _, err := parsed.DockerImage(); err == nil {
			t.Fatal("selected an unpinned image from an unvalidated manifest")
		}
	}
}

func TestManifestSizeLimit(t *testing.T) {
	valid := manifestHeader + manifestNative
	data := valid + "#" + strings.Repeat("a", manifest.MaxBytes-len(valid)-1)
	if _, err := manifest.Parse([]byte(data), "v0.1.0"); err != nil {
		t.Fatalf("manifest at size limit: %v", err)
	}
	if _, err := manifest.Parse([]byte(data+"a"), "v0.1.0"); err == nil {
		t.Fatal("accepted oversized manifest")
	}
}
