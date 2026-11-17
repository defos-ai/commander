package fixtures

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/comms/messages"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

func ReportedAgentVersions() []struct{ Name, Version string } {
	return []struct{ Name, Version string }{
		{Name: "unreported"},
		{Name: "release", Version: "v1.2.3"},
		{Name: "custom build at byte limit", Version: "dev-" + strings.Repeat("界", 40) + "+sha"},
	}
}

func InvalidAgentVersions() []struct{ Name, Version string } {
	return []struct{ Name, Version string }{
		{Name: "leading whitespace", Version: " v1.2.3"},
		{Name: "embedded whitespace", Version: "dev build"},
		{Name: "Unicode whitespace", Version: "dev\u00a0build"},
		{Name: "ASCII control", Version: "dev\x00build"},
		{Name: "Unicode control", Version: "dev\u009fbuild"},
		{Name: "oversized UTF-8", Version: strings.Repeat("界", 43)},
		{Name: "invalid UTF-8", Version: string([]byte{'v', 0xff})},
	}
}

func VersionConfig(version string) agent.AgentConfig {
	return agent.AgentConfig{Type: "fixture", Version: version,
		Server:            agent.ServerInfo{IPAddress: "127.0.0.1", Hostname: "version-fixture"},
		ArtifactFileStore: fakes.UnusedArtifacts{},
	}
}

func WithRawAgentVersion(t *testing.T, announcement messages.Message, version string) messages.Message {
	t.Helper()
	encoded, err := json.Marshal(version)
	if err != nil {
		t.Fatal(err)
	}
	field := append([]byte(`"version":`), encoded...)
	if bytes.Count(announcement.Payload, field) != 1 {
		t.Fatal("announcement must contain exactly one matching version field")
	}
	// Marshal normalizes malformed UTF-8; restore the original wire bytes afterward.
	raw := append([]byte(`"version":"`), []byte(version)...)
	raw = append(raw, '"')
	announcement.Payload = bytes.Replace(announcement.Payload, field, raw, 1)
	return announcement
}
