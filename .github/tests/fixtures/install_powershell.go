package fixtures

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type PowerShellInstaller struct {
	Directory, Target, Asset string
	executable, repository   string
}

const InstallerBinary = "Commander installer fixture bytes\n"

type PowerShellRuntime struct {
	Name, Executable string
}

func PowerShellRuntimes(t *testing.T) []PowerShellRuntime {
	t.Helper()
	var runtimes []PowerShellRuntime
	for _, name := range []string{"pwsh", "powershell"} {
		if executable, err := exec.LookPath(name); err == nil {
			runtimes = append(runtimes, PowerShellRuntime{Name: name, Executable: executable})
		}
	}
	if len(runtimes) == 0 {
		if runtime.GOOS == "windows" {
			t.Fatal("PowerShell must be available to verify the Windows installer")
		}
		t.Skip("PowerShell installer flows run on native Windows runners")
	}
	return runtimes
}

func NewPowerShellInstaller(t *testing.T, architecture string, shell PowerShellRuntime) *PowerShellInstaller {
	t.Helper()
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	f := &PowerShellInstaller{Directory: t.TempDir(), executable: shell.Executable, repository: repository}
	arch := map[string]string{"AMD64": "amd64", "ARM64": "arm64"}[architecture]
	f.Asset = "commander_windows_" + arch + ".exe"
	f.Target = filepath.Join(f.Directory, "AppData", "Commander", "bin", "commander.exe")
	f.Write(t, "download", InstallerBinary)
	f.Write(t, "checksums.txt", fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(InstallerBinary)), f.Asset))
	f.Write(t, "latest-url", "https://github.com/defos-ai/commander/releases/tag/v1.2.3")
	return f
}

func (f *PowerShellInstaller) Run(t *testing.T, architecture string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File",
		filepath.Join(f.repository, ".github", "tests", "fixtures", "install_powershell.ps1"),
		"-Repository", f.repository, "-Fixture", f.Directory, "-Architecture", architecture}
	command := exec.CommandContext(ctx, f.executable, append(arguments, args...)...)
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	return string(output), err
}

func (f *PowerShellInstaller) Write(t *testing.T, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.Directory, name), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *PowerShellInstaller) Existing(t *testing.T, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.Target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Target, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *PowerShellInstaller) LinkedTarget(t *testing.T, value string) string {
	t.Helper()
	f.Existing(t, value)
	if err := os.Remove(f.Target); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(f.Directory, "other.exe")
	if err := os.WriteFile(other, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, f.Target); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	return other
}

func (f *PowerShellInstaller) Read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.Directory, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
