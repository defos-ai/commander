package packages

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

const MaxSourcesBytes = 64 << 10

type Sources struct {
	SchemaVersion int              `yaml:"schema_version"`
	Agents        map[string]Entry `yaml:"agents"`
}

type Entry struct {
	Description string     `yaml:"description"`
	Source      string     `yaml:"source"`
	Visibility  Visibility `yaml:"visibility"`
}

// Visibility describes repository access, not package trust or image visibility.
type Visibility string

const (
	Public  Visibility = "public"
	Private Visibility = "private"
)

var aliasPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ParseSources accepts one strict sources document without YAML scalar coercion.
func ParseSources(data []byte) (Sources, error) {
	if len(data) > MaxSourcesBytes {
		return Sources{}, fmt.Errorf("agent sources exceed %d bytes", MaxSourcesBytes)
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return Sources{}, fmt.Errorf("decode agent sources: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return Sources{}, errors.New("agent sources must be a mapping")
	}
	if err := validateYAML(document.Content[0], 0); err != nil {
		return Sources{}, fmt.Errorf("invalid agent sources: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Sources{}, errors.New("agent sources must contain exactly one YAML document")
	}

	var sources Sources
	decoder = yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&sources); err != nil {
		return Sources{}, fmt.Errorf("decode agent sources fields: %w", err)
	}
	if err := sources.Validate(); err != nil {
		return Sources{}, err
	}
	return sources, nil
}

// Reject aliases, duplicate keys and values that typed YAML decoding would coerce.
func validateYAML(node *yaml.Node, depth int) error {
	if depth > 16 {
		return errors.New("sources nesting is too deep")
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
				return errors.New("mapping keys must be strings")
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

func (c Sources) Validate() error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("unsupported agent sources schema_version %d; expected 1", c.SchemaVersion)
	}
	if c.Agents == nil {
		return errors.New("agent sources needs an agents mapping")
	}
	for name, entry := range c.Agents {
		if !aliasPattern.MatchString(name) {
			return fmt.Errorf("invalid agent alias %q; expected lowercase letters, digits or hyphens starting with a letter", name)
		}
		if entry.Description == "" || strings.TrimSpace(entry.Description) != entry.Description {
			return fmt.Errorf("agent alias %q needs a nonempty trimmed description", name)
		}
		if entry.Visibility != Public && entry.Visibility != Private {
			return fmt.Errorf("agent alias %q visibility must be public or private", name)
		}
		if _, err := ParseSelector(entry.Source); err != nil {
			return fmt.Errorf("agent alias %q: %w", name, err)
		}
	}
	return nil
}

func (c Sources) Lookup(name string) (Entry, error) {
	if err := c.Validate(); err != nil {
		return Entry{}, err
	}
	entry, ok := c.Agents[name]
	if !ok {
		return Entry{}, fmt.Errorf("agent alias %q is not in the sources", name)
	}
	return entry, nil
}
