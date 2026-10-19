package packages

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/defos-ai/commander/framework/id"
	"github.com/distribution/reference"
	"go.yaml.in/yaml/v3"
)

const MaxManifestBytes = 64 << 10

type Manifest struct {
	SchemaVersion int                    `yaml:"schema_version"`
	Type          string                 `yaml:"type"`
	Version       string                 `yaml:"version"`
	Native        map[string]NativeAsset `yaml:"native"`
	Docker        *Docker                `yaml:"docker"`
}

type NativeAsset struct {
	Asset  string `yaml:"asset"`
	SHA256 string `yaml:"sha256"`
}

type Docker struct {
	Image string `yaml:"image"`
}

var (
	platformPattern = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)
	assetPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// ParseManifest accepts one strict release manifest matching the requested tag.
func ParseManifest(data []byte, version string) (Manifest, error) {
	if len(data) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("agent manifest exceeds %d bytes", MaxManifestBytes)
	}
	if !validVersion(version) {
		return Manifest{}, errors.New("requested version must be a nonzero stable vMAJOR.MINOR.PATCH tag")
	}

	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return Manifest{}, fmt.Errorf("decode agent manifest: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return Manifest{}, errors.New("agent manifest must be a mapping")
	}
	if err := validateYAML(document.Content[0], 0); err != nil {
		return Manifest{}, fmt.Errorf("invalid agent manifest: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("agent manifest must contain exactly one YAML document")
	}

	var manifest Manifest
	decoder = yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode agent manifest fields: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	if manifest.Version != version {
		return Manifest{}, fmt.Errorf("agent manifest version %q does not match requested version %q", manifest.Version, version)
	}
	return manifest, nil
}

// YAML's normal typed decoding coerces some scalar values into strings. Reject
// those coercions, aliases and duplicate keys before decoding the known fields.
func validateYAML(node *yaml.Node, depth int) error {
	if depth > 16 {
		return errors.New("manifest nesting is too deep")
	}
	if node.Anchor != "" || node.Kind == yaml.AliasNode {
		return errors.New("YAML anchors and aliases are not supported")
	}
	switch node.Kind {
	case yaml.MappingNode:
		if node.Tag != "!!map" {
			return errors.New("unsupported mapping tag")
		}
		seen := make(map[string]bool, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Anchor != "" {
				return errors.New("mapping keys must be plain strings")
			}
			if seen[key.Value] {
				return fmt.Errorf("duplicate key %q", key.Value)
			}
			seen[key.Value] = true
			if depth == 0 && key.Value == "schema_version" {
				if value.Kind != yaml.ScalarNode || value.Tag != "!!int" || value.Anchor != "" {
					return errors.New("schema_version must be an integer")
				}
				continue
			}
			if err := validateYAML(value, depth+1); err != nil {
				return fmt.Errorf("%s: %w", key.Value, err)
			}
		}
	case yaml.ScalarNode:
		if node.Tag != "!!str" {
			return errors.New("expected a string or mapping")
		}
	default:
		return errors.New("expected a string or mapping")
	}
	return nil
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported agent manifest schema_version %d; expected 1", m.SchemaVersion)
	}
	// Check the eventual runtime ID shape without allocating an agent identity.
	runtimeType := strings.TrimSuffix(m.Type, "-agent")
	if !id.ValidType(m.Type) || !id.ValidRuntime(runtimeType+"-agent-00000000", id.Agent) {
		return errors.New("agent manifest type cannot form a valid agent ID")
	}
	if !validVersion(m.Version) {
		return errors.New("agent manifest version must be a nonzero stable vMAJOR.MINOR.PATCH tag")
	}
	if m.Native == nil && m.Docker == nil {
		return errors.New("agent manifest needs a native or Docker delivery mode")
	}
	if m.Native != nil && len(m.Native) == 0 {
		return errors.New("agent manifest native mode must contain at least one platform")
	}
	for platform, asset := range m.Native {
		if !platformPattern.MatchString(platform) {
			return fmt.Errorf("invalid native platform %q; expected GOOS/GOARCH", platform)
		}
		// IsLocal also rejects Windows device names, which can match the regex.
		if !assetPattern.MatchString(asset.Asset) || !filepath.IsLocal(asset.Asset) || strings.HasSuffix(asset.Asset, ".") {
			return fmt.Errorf("native platform %q needs a safe asset basename", platform)
		}
		if !validSHA256(asset.SHA256) {
			return fmt.Errorf("native platform %q needs a 64-character lowercase SHA-256 checksum", platform)
		}
	}
	if m.Docker != nil {
		if err := validateImage(m.Docker.Image); err != nil {
			return err
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validateImage(image string) error {
	name, digest, ok := strings.Cut(image, "@sha256:")
	if !ok || !validSHA256(digest) {
		return errors.New("Docker image must be pinned by a lowercase SHA-256 digest")
	}
	parsed, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return fmt.Errorf("invalid Docker image reference: %w", err)
	}
	if _, tagged := parsed.(reference.Tagged); tagged {
		return errors.New("Docker image must use a digest without a tag")
	}
	if !strings.HasPrefix(name, reference.Domain(parsed)+"/") {
		return errors.New("Docker image must include its registry")
	}
	return nil
}

func (m Manifest) NativeFor(goos, goarch string) (NativeAsset, error) {
	if err := m.Validate(); err != nil {
		return NativeAsset{}, err
	}
	platform := goos + "/" + goarch
	asset, ok := m.Native[platform]
	if !ok {
		return NativeAsset{}, fmt.Errorf("agent package has no native executable for %s", platform)
	}
	return asset, nil
}

func (m Manifest) DockerImage() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	if m.Docker == nil {
		return "", errors.New("agent package has no Docker image")
	}
	return m.Docker.Image, nil
}
