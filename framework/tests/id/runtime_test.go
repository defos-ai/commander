package id_test

import (
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/id"
)

func TestAgentIDFromSuffix(t *testing.T) {
	for _, agentType := range []string{"browser", "browser-agent", "custom-browser-agent", strings.Repeat("a", 48)} {
		for _, suffix := range []string{"00000000", "ffffffff", "a1b2c3d4"} {
			value, err := id.AgentIDFromSuffix(agentType, suffix)
			want := strings.TrimSuffix(agentType, "-agent") + "-agent-" + suffix
			if err != nil || value != want || !id.ValidAgent(value) {
				t.Fatalf("AgentIDFromSuffix(%q, %q) = %q, %v; want %q", agentType, suffix, value, err, want)
			}
		}
	}
}

func TestAgentIDFromSuffixRejectsExternalPrefixesAndInvalidSuffixes(t *testing.T) {
	for _, suffix := range []string{"", "abcdefg1", "ABCDEF12", "1234567", "123456789", "browser-agent-a1b2c3d4", "../12345", "1234567\n", " 1234567", "éééé", "1234567\x00"} {
		if value, err := id.AgentIDFromSuffix("browser-agent", suffix); err == nil || value != "" {
			t.Errorf("accepted invalid suffix %q: %q, %v", suffix, value, err)
		}
	}
	for _, agentType := range []string{"", "Browser", "../browser", "browser--test", strings.Repeat("a", 49)} {
		if value, err := id.AgentIDFromSuffix(agentType, "a1b2c3d4"); err == nil || value != "" {
			t.Errorf("accepted invalid type %q: %q, %v", agentType, value, err)
		}
	}
}
