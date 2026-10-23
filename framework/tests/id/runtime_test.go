package id_test

import (
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/id"
)

func TestAgentIDFromSuffix(t *testing.T) {
	for _, test := range []struct{ agentType, suffix, want string }{
		{"browser", "00000000", "browser-agent-00000000"},
		{"browser-agent", "ffffffff", "browser-agent-ffffffff"},
		{"custom-browser-agent", "a1b2c3d4", "custom-browser-agent-a1b2c3d4"},
		{strings.Repeat("a", 48), "a1b2c3d4", strings.Repeat("a", 48) + "-agent-a1b2c3d4"},
	} {
		value, err := id.AgentIDFromSuffix(test.agentType, test.suffix)
		if err != nil || value != test.want || !id.ValidAgent(value) {
			t.Fatalf("AgentIDFromSuffix(%q, %q) = %q, %v; want %q", test.agentType, test.suffix, value, err, test.want)
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
