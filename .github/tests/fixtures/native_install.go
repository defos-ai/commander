package fixtures

import (
	"crypto/sha256"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

func InstallNativeCommander(t *testing.T, source string) string {
	t.Helper()
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Fatalf("unsupported native installer architecture %s", runtime.GOARCH)
	}
	binary, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read native Commander binary: %v", err)
	}
	if runtime.GOOS == "windows" {
		architecture := strings.ToUpper(runtime.GOARCH)
		installer := NewPowerShellInstaller(t, architecture, PowerShellRuntimes(t)[0])
		installer.Write(t, "download", string(binary))
		installer.Write(t, "checksums.txt", fmt.Sprintf("%x  %s\n", sha256.Sum256(binary), installer.Asset))
		if output, err := installer.Run(t, architecture, "-Version", "v1.2.3"); err != nil {
			t.Fatalf("install native Commander: %v: %s", err, output)
		}
		return installer.Target
	}
	platform := map[string]string{"linux": "Linux", "darwin": "Darwin"}[runtime.GOOS]
	if platform == "" {
		t.Fatalf("unsupported native installer platform %s", runtime.GOOS)
	}
	installer := NewShellInstaller(t)
	installer.SetEnvironment("INSTALLER_TEST_OS", platform)
	installer.SetEnvironment("INSTALLER_TEST_ARCH", runtime.GOARCH)
	asset := "commander_" + runtime.GOOS + "_" + runtime.GOARCH
	installer.WriteAsset(t, asset, binary)
	installer.WriteAsset(t, "checksums.txt", fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(binary), asset))
	if output, err := installer.Run(t, "--version", "v1.2.3"); err != nil {
		t.Fatalf("install native Commander: %v: %s", err, output)
	}
	return installer.Destination()
}
