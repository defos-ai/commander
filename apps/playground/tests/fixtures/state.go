package fixtures

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/state"
)

func ProfileDirectory(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "profile")
}

func OpenProfile(t *testing.T, directory string) *state.Profile {
	t.Helper()
	profile, err := state.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := profile.Close(); err != nil {
			t.Error(err)
		}
	})
	return profile
}

func ProfileFile(t *testing.T, directory, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func ProfileSettings() state.Settings {
	return state.Settings{
		Provider: "openai", Model: "fixture-model", BaseURL: "https://provider.example/v1",
		Remote: state.ConnectionSettings{
			MCPURL: "https://commander.example/mcp", APIURL: "https://commander.example/api", APIUsername: "operator",
		},
	}
}

func ProfileLockProbe(directory string) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestProfileExcludesAnotherProcess$", "-test.timeout=10s")
	command.Env = append(os.Environ(), "COMMANDER_TEST_PROFILE_LOCK="+directory)
	return command
}
