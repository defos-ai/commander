package packages_test

import (
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	fixtures.CleanupNativeBuild()
	os.Exit(code)
}
