package tests

import (
	"os"
	"testing"

	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestMain(tests *testing.M) {
	if exitCode, child := fixtures.RunBackgroundChild(); child {
		os.Exit(exitCode)
	}
	os.Exit(tests.Run())
}
