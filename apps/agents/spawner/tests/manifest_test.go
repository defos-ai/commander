package packages_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
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
			manifest, err := packages.ParseManifest([]byte(manifestHeader+mode.body), "v0.1.0")
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Type != "example-agent" || manifest.Version != "v0.1.0" {
				t.Fatalf("unexpected package identity: %#v", manifest)
			}
			asset, err := manifest.NativeFor("linux", "amd64")
			if mode.native {
				if err != nil || asset.Asset != "commander-example-agent_linux_amd64" || asset.SHA256 != manifestChecksum {
					t.Fatalf("native selection: %#v, %v", asset, err)
				}
			} else if err == nil {
				t.Fatal("selected unavailable native mode")
			}
			image, err := manifest.DockerImage()
			if mode.docker {
				if err != nil || image != manifestImage {
					t.Fatalf("Docker selection: %q, %v", image, err)
				}
			} else if err == nil {
				t.Fatal("selected unavailable Docker mode")
			}
			if _, err := manifest.NativeFor("darwin", "arm64"); err == nil {
				t.Fatal("selected unavailable native platform")
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := packages.ParseManifest([]byte(tc.data), "v0.1.0"); err == nil {
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
			if _, err := packages.ParseManifest([]byte(data), "v0.1.0"); err == nil {
				t.Fatal("accepted invalid agent type")
			}
		})
	}
	for _, version := range []string{"", "latest", "stable", "0.1.0", "v0.0.0", "v01.0.0", "v1.2", "v1.2.3-rc.1", "v1.2.3+build.1"} {
		t.Run("version/"+version, func(t *testing.T) {
			data := strings.Replace(valid, "version: v0.1.0", "version: \""+version+"\"", 1)
			if _, err := packages.ParseManifest([]byte(data), version); err == nil {
				t.Fatal("accepted invalid release version")
			}
		})
	}
	if _, err := packages.ParseManifest([]byte(valid), "v0.2.0"); err == nil {
		t.Fatal("accepted manifest from another release")
	}
	maxType := strings.Repeat("a", 48) + "-agent"
	data := strings.Replace(valid, "type: example-agent", "type: "+maxType, 1)
	if _, err := packages.ParseManifest([]byte(data), "v0.1.0"); err != nil {
		t.Fatalf("rejected type producing a 63-byte agent ID: %v", err)
	}
}

func TestManifestRejectsUnsafeNativeAssets(t *testing.T) {
	valid := manifestHeader + manifestNative
	for _, asset := range []string{"", ".", "..", ".hidden", "agent.", "agent..", "../agent", "/tmp/agent", `..\agent`, `C:\agent.exe`, "https://example.org/agent", "agent file", "agent?token=secret", "a%2fb", "agént", "agent\nfile"} {
		t.Run(asset, func(t *testing.T) {
			manifest, err := packages.ParseManifest([]byte(valid), "v0.1.0")
			if err != nil {
				t.Fatal(err)
			}
			manifest.Native["linux/amd64"] = packages.NativeAsset{Asset: asset, SHA256: manifestChecksum}
			if err := manifest.Validate(); err == nil {
				t.Fatal("accepted unsafe asset filename")
			}
			if _, err := manifest.NativeFor("linux", "amd64"); err == nil {
				t.Fatal("selected unsafe asset from an unvalidated manifest")
			}
		})
	}
	for _, checksum := range []string{"", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		data := strings.Replace(valid, manifestChecksum, checksum, 1)
		if _, err := packages.ParseManifest([]byte(data), "v0.1.0"); err == nil {
			t.Errorf("accepted invalid checksum %q", checksum)
		}
	}
	for _, platform := range []string{"linux", "linux/amd64/extra", "Linux/amd64", "linux/../amd64"} {
		data := strings.Replace(valid, "linux/amd64:", platform+":", 1)
		if _, err := packages.ParseManifest([]byte(data), "v0.1.0"); err == nil {
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
			if _, err := packages.ParseManifest([]byte(data), "v0.1.0"); err == nil {
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
			if _, err := packages.ParseManifest([]byte(data), "v0.1.0"); err == nil {
				t.Fatal("accepted unsafe Docker image reference")
			}
		})
	}
	for _, image := range []string{"ghcr.io/defos-ai/example@sha256:" + digest, "registry.example.org:5000/team/example@sha256:" + digest, "docker.io/library/example@sha256:" + digest} {
		data := manifestHeader + "docker:\n  image: " + image + "\n"
		manifest, err := packages.ParseManifest([]byte(data), "v0.1.0")
		if err != nil {
			t.Fatalf("rejected pinned image %q: %v", image, err)
		}
		manifest.Docker.Image = "ghcr.io/defos-ai/example:latest"
		if _, err := manifest.DockerImage(); err == nil {
			t.Fatal("selected an unpinned image from an unvalidated manifest")
		}
	}
}

func TestManifestSizeLimit(t *testing.T) {
	valid := manifestHeader + manifestNative
	data := valid + "#" + strings.Repeat("a", packages.MaxManifestBytes-len(valid)-1)
	if _, err := packages.ParseManifest([]byte(data), "v0.1.0"); err != nil {
		t.Fatalf("manifest at size limit: %v", err)
	}
	if _, err := packages.ParseManifest([]byte(data+"a"), "v0.1.0"); err == nil {
		t.Fatal("accepted oversized manifest")
	}
}
