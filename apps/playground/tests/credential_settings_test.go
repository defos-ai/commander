package playground_test

import (
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/credentials"
)

func TestContainerCredentialStateSettings(t *testing.T) {
	for _, scenario := range []struct {
		name, directory string
		arguments       []string
		environment     map[string]string
		temporary       bool
		invalid         bool
	}{
		{name: "temporary flag", arguments: []string{"--all-in-one", "--temporary"}, temporary: true},
		{name: "last boolean wins", arguments: []string{"--temporary=true", "--temporary=false"}},
		{name: "environment fallback", environment: map[string]string{"COMMANDER_TEMPORARY": "true", "COMMANDER_STATE_DIR": "/saved"}, directory: "/saved", temporary: true},
		{name: "explicit false overrides environment", arguments: []string{"--temporary=false"}, environment: map[string]string{"COMMANDER_TEMPORARY": "true"}},
		{name: "explicit flag ignores invalid environment", arguments: []string{"--temporary=false"}, environment: map[string]string{"COMMANDER_TEMPORARY": "private-invalid"}},
		{name: "separate directory", arguments: []string{"--state-dir", "/saved"}, directory: "/saved"},
		{name: "directory assignment", arguments: []string{"--state-dir=/first", "--state-dir=/saved"}, directory: "/saved"},
		{name: "string values are consumed", arguments: []string{"--state-dir", "--temporary", "--model", "--temporary"}, directory: "--temporary"},
		{name: "missing directory", arguments: []string{"--state-dir"}, invalid: true},
		{name: "invalid temporary", arguments: []string{"--temporary=private-invalid"}, invalid: true},
		{name: "invalid environment", environment: map[string]string{"COMMANDER_TEMPORARY": "private-invalid"}, invalid: true},
		{name: "unknown flag", arguments: []string{"--private-unknown"}, invalid: true},
		{name: "positional input stops parsing", arguments: []string{"private-positional", "--temporary"}, invalid: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory, temporary, err := credentials.ContainerState(scenario.arguments, scenario.environment)

			if scenario.invalid {
				if err == nil || strings.Contains(err.Error(), "private-") {
					t.Fatal("invalid startup settings did not fail with a redacted error")
				}
				return
			}
			if err != nil || directory != scenario.directory || temporary != scenario.temporary {
				t.Fatalf("startup settings mismatch: directory=%q temporary=%t err=%v", directory, temporary, err)
			}
		})
	}
}
