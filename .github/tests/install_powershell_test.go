package release_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defos-ai/commander/.github/tests/fixtures"
)

func TestCIInstallersPowerShellPreviewAndVerifiedInstallation(t *testing.T) {
	t.Setenv("PSModulePath", filepath.Join(t.TempDir(), "parent-shell-modules"))
	for _, shell := range fixtures.PowerShellRuntimes(t) {
		for _, architecture := range []string{"AMD64", "ARM64"} {
			t.Run(shell.Name+"/"+architecture, func(t *testing.T) {
				fixture := fixtures.NewPowerShellInstaller(t, architecture, shell)

				output, err := fixture.Run(t, architecture, "-Preview")

				if err != nil || !strings.Contains(output, "v1.2.3/"+fixture.Asset) {
					t.Fatalf("preview did not resolve a concrete native asset: %v: %s", err, output)
				}
				if _, err := os.Stat(fixture.Target); !os.IsNotExist(err) {
					t.Fatal("preview created the install destination")
				}
				if requests := fixture.Read(t, "requests.log"); strings.Count(requests, "\n") != 1 || !strings.HasPrefix(requests, "Head ") {
					t.Fatalf("preview performed install downloads: %s", requests)
				}

				output, err = fixture.Run(t, architecture)

				if err != nil {
					t.Fatalf("verified installation failed: %v: %s", err, output)
				}
				assertPowerShellTarget(t, fixture.Target, fixtures.InstallerBinary)
				before, err := os.Stat(fixture.Target)
				if err != nil {
					t.Fatal(err)
				}
				requests := fixture.Read(t, "requests.log")
				if strings.Count(requests, "Head https://github.com/defos-ai/commander/releases/latest") != 2 || strings.Contains(requests, "latest/download") {
					t.Fatalf("installation did not pin its single resolved release: %s", requests)
				}

				output, err = fixture.Run(t, architecture, "-Version", "v1.2.3")

				if err != nil || !strings.Contains(output, "already installed") {
					t.Fatalf("identical installation was not idempotent: %v: %s", err, output)
				}
				after, err := os.Stat(fixture.Target)
				if err != nil || !after.ModTime().Equal(before.ModTime()) {
					t.Fatal("identical installation rewrote the binary")
				}
				binaryRequest := "Get https://github.com/defos-ai/commander/releases/download/v1.2.3/" + fixture.Asset + "\n"
				if strings.Count(fixture.Read(t, "requests.log"), binaryRequest) != strings.Count(requests, binaryRequest) {
					t.Fatal("identical installation downloaded the binary again")
				}
			})
		}
	}
}

func TestCIInstallersPowerShellPreservesFilesBeforeVerifiedReplacement(t *testing.T) {
	for _, shell := range fixtures.PowerShellRuntimes(t) {
		t.Run(shell.Name, func(t *testing.T) {
			powerShellVerifiedReplacement(t, shell)
		})
	}
}

func powerShellVerifiedReplacement(t *testing.T, shell fixtures.PowerShellRuntime) {
	fixture := fixtures.NewPowerShellInstaller(t, "AMD64", shell)
	fixture.Existing(t, "existing Commander bytes")
	fixture.Write(t, "download", "corrupted download")

	output, err := fixture.Run(t, "AMD64", "-Version", "v1.2.3", "-Replace")

	if err == nil || !strings.Contains(output, "checksum verification failed") {
		t.Fatalf("corrupt bytes were accepted: %v: %s", err, output)
	}
	assertPowerShellTarget(t, fixture.Target, "existing Commander bytes")
	fixture.Write(t, "download", fixtures.InstallerBinary)

	output, err = fixture.Run(t, "AMD64", "-Version", "v1.2.3")

	if err == nil || !strings.Contains(output, "-Force") {
		t.Fatalf("different installed bytes were replaced without approval: %v: %s", err, output)
	}
	assertPowerShellTarget(t, fixture.Target, "existing Commander bytes")

	output, err = fixture.Run(t, "AMD64", "-Version", "v1.2.3", "-Replace")

	if err != nil {
		t.Fatalf("approved verified replacement failed: %v: %s", err, output)
	}
	assertPowerShellTarget(t, fixture.Target, fixtures.InstallerBinary)
	manifest := fmt.Sprintf("%x  %s\ninvalid  %s\n", sha256.Sum256([]byte(fixtures.InstallerBinary)), fixture.Asset, fixture.Asset)
	fixture.Write(t, "checksums.txt", manifest)

	output, err = fixture.Run(t, "AMD64", "-Version", "v1.2.3", "-Replace")

	if err == nil || !strings.Contains(output, "malformed checksum") {
		t.Fatalf("malformed duplicate target checksum was accepted: %v: %s", err, output)
	}
	assertPowerShellTarget(t, fixture.Target, fixtures.InstallerBinary)
}

func TestCIInstallersPowerShellRejectsUnsafeRedirectsAndDestinations(t *testing.T) {
	for _, shell := range fixtures.PowerShellRuntimes(t) {
		t.Run(shell.Name, func(t *testing.T) {
			powerShellRejectsUnsafeTargets(t, shell)
		})
	}
}

func powerShellRejectsUnsafeTargets(t *testing.T, shell fixtures.PowerShellRuntime) {
	fixture := fixtures.NewPowerShellInstaller(t, "AMD64", shell)
	fixture.Write(t, "latest-url", "http://github.com/defos-ai/commander/releases/tag/v1.2.3")

	output, err := fixture.Run(t, "AMD64", "-Preview")

	if err == nil || !strings.Contains(output, "unsafe redirect") {
		t.Fatalf("HTTP downgrade was accepted: %v: %s", err, output)
	}
	if _, err := os.Stat(fixture.Target); !os.IsNotExist(err) {
		t.Fatal("unsafe redirect created an install destination")
	}
	fixture.Write(t, "latest-url", "https://github.com/defos-ai/commander/releases/tag/v1.2.3")
	other := fixture.LinkedTarget(t, "linked bytes")

	output, err = fixture.Run(t, "AMD64", "-Version", "v1.2.3", "-Replace")

	if err == nil || !strings.Contains(output, "reparse point") {
		t.Fatalf("linked install destination was replaced: %v: %s", err, output)
	}
	assertPowerShellTarget(t, other, "linked bytes")
}

func assertPowerShellTarget(t *testing.T, path, expected string) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != expected {
		t.Fatalf("installed bytes changed unexpectedly: %v", err)
	}
}
