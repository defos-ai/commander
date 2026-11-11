package release_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/defos-ai/commander/.github/tests/fixtures"
)

func TestCIInstallersShellPreviewSelectsOnePinnedReleaseWithoutInstalling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses the PowerShell installer")
	}
	for _, platform := range []struct{ os, arch, asset string }{
		{"Linux", "x86_64", "commander_linux_amd64"},
		{"Linux", "aarch64", "commander_linux_arm64"},
		{"Darwin", "x86_64", "commander_darwin_amd64"},
		{"Darwin", "arm64", "commander_darwin_arm64"},
	} {
		t.Run(platform.asset, func(t *testing.T) {
			installer := fixtures.NewShellInstaller(t)
			installer.SetEnvironment("INSTALLER_TEST_OS", platform.os)
			installer.SetEnvironment("INSTALLER_TEST_ARCH", platform.arch)

			output, err := installer.Run(t, "--dry-run")

			if err != nil || !strings.Contains(output, "/releases/download/v1.2.3/"+platform.asset) {
				t.Fatalf("preview did not resolve the correct release binary: %v: %s", err, output)
			}
			if _, err := os.Stat(installer.BinDir); !os.IsNotExist(err) {
				t.Fatal("preview created the installation directory")
			}
			if urls := installer.URLs(t); len(urls) != 1 || urls[0] != "https://github.com/defos-ai/commander/releases/latest" {
				t.Fatalf("preview must resolve latest exactly once without downloading assets: %v", urls)
			}
		})
	}
}

func TestCIInstallersShellVerifiesBeforeReplacementAndLeavesExistingFilesSafe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses the PowerShell installer")
	}
	installer := fixtures.NewShellInstaller(t)
	if err := os.Mkdir(installer.BinDir, 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte("retained local executable\n")
	if err := os.WriteFile(installer.Destination(), old, 0700); err != nil {
		t.Fatal(err)
	}

	output, err := installer.Run(t, "--version", "v1.2.3")

	if err == nil || !strings.Contains(output, "--force") {
		t.Fatalf("replacement did not require explicit authorization: %v: %s", err, output)
	}
	assertInstalledBytes(t, installer.Destination(), old)
	installer.WriteAsset(t, "commander_linux_amd64", []byte("corrupt download"))

	output, err = installer.Run(t, "--version", "v1.2.3", "--force")

	if err == nil || !strings.Contains(output, "checksum") {
		t.Fatalf("a corrupt download was installed: %v: %s", err, output)
	}
	assertInstalledBytes(t, installer.Destination(), old)
	installer.WriteAsset(t, "commander_linux_amd64", installer.Binary)

	output, err = installer.Run(t, "--version", "v1.2.3", "--force")

	if err != nil {
		t.Fatalf("verified installation failed: %v: %s", err, output)
	}
	assertInstalledBytes(t, installer.Destination(), installer.Binary)
	info, err := os.Stat(installer.Destination())
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("installed binary was not executable")
	}

	output, err = installer.Run(t, "--version", "v1.2.3")

	if err != nil || !strings.Contains(output, "already installed") {
		t.Fatalf("an identical installation was not idempotent: %v: %s", err, output)
	}
	if installer.Executed() {
		t.Fatal("installer executed the downloaded binary")
	}
	for _, url := range installer.URLs(t) {
		if !strings.HasPrefix(url, "https://github.com/defos-ai/commander/releases/download/v1.2.3/") {
			t.Fatalf("pinned installation fetched an unpinned release: %s", url)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(installer.Directory, "commander-install.*")); err != nil || len(matches) != 0 {
		t.Fatal("installation left temporary downloads behind")
	}
}

func TestCIInstallersShellRejectsAmbiguousChecksumsAndSymlinkTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses the PowerShell installer")
	}
	installer := fixtures.NewShellInstaller(t)
	checksums, err := os.ReadFile(filepath.Join(installer.Assets, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	installer.WriteAsset(t, "checksums.txt", append(checksums, checksums...))

	output, err := installer.Run(t)

	if err == nil || !strings.Contains(output, "exactly one") {
		t.Fatalf("ambiguous release checksums were accepted: %v: %s", err, output)
	}
	if _, err := os.Stat(installer.BinDir); !os.IsNotExist(err) {
		t.Fatal("invalid release metadata created an installation")
	}
	urls := installer.URLs(t)
	if len(urls) != 2 || !strings.HasSuffix(urls[1], "/download/v1.2.3/checksums.txt") {
		t.Fatalf("latest resolution was not pinned before asset download: %v", urls)
	}
	installer.WriteAsset(t, "checksums.txt", append(append([]byte(nil), checksums...), []byte("bad extra commander_linux_amd64\n")...))

	output, err = installer.Run(t, "--version", "v1.2.3")

	if err == nil || !strings.Contains(output, "exactly one") {
		t.Fatalf("malformed target checksum lines were ignored: %v: %s", err, output)
	}
	if _, err := os.Stat(installer.BinDir); !os.IsNotExist(err) {
		t.Fatal("malformed release metadata created an installation")
	}
	if err := os.Mkdir(installer.BinDir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(installer.Directory, "unrelated")
	value := []byte("keep this file")
	if err := os.WriteFile(target, value, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, installer.Destination()); err != nil {
		t.Fatal(err)
	}

	output, err = installer.Run(t, "--version", "v1.2.3", "--force")

	if err == nil || !strings.Contains(output, "symbolic link") {
		t.Fatalf("a symlink destination was replaced: %v: %s", err, output)
	}
	assertInstalledBytes(t, target, value)
}

func assertInstalledBytes(t *testing.T, path string, expected []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatalf("installed file changed unexpectedly: %v", err)
	}
}
