package fixtures

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/.github/tests/fakes"
)

type ShellInstaller struct {
	Directory string
	BinDir    string
	Assets    string
	Binary    []byte
	script    string
	env       map[string]string
}

func NewShellInstaller(t *testing.T) *ShellInstaller {
	t.Helper()
	directory := t.TempDir()
	f := &ShellInstaller{Directory: directory, BinDir: filepath.Join(directory, "local bin"), Assets: filepath.Join(directory, "assets")}
	var err error
	f.script, err = filepath.Abs("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	tools := filepath.Join(directory, "tools")
	for _, path := range []string{tools, f.Assets} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"curl": fakes.InstallerCurl, "uname": fakes.InstallerUname} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.Binary = []byte("#!/bin/sh\nprintf 'unexpected execution' > \"$INSTALLER_TEST_EXECUTED\"\n")
	f.env = map[string]string{
		"PATH": tools + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME": directory, "TMPDIR": directory,
		"INSTALLER_TEST_ASSETS": f.Assets,
		"INSTALLER_TEST_CALLS":  filepath.Join(directory, "curl-calls"),
		"INSTALLER_TEST_URLS":   filepath.Join(directory, "curl-urls"),
		"INSTALLER_TEST_LATEST": "https://github.com/defos-ai/commander/releases/tag/v1.2.3",
		"INSTALLER_TEST_OS":     "Linux", "INSTALLER_TEST_ARCH": "x86_64",
		"INSTALLER_TEST_EXECUTED": filepath.Join(directory, "executed"),
	}
	var checksums strings.Builder
	for _, name := range []string{"commander_linux_amd64", "commander_linux_arm64", "commander_darwin_amd64", "commander_darwin_arm64"} {
		f.WriteAsset(t, name, f.Binary)
		fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256(f.Binary), name)
	}
	f.WriteAsset(t, "checksums.txt", []byte(checksums.String()))
	return f
}

func (f *ShellInstaller) SetEnvironment(name, value string) { f.env[name] = value }

func (f *ShellInstaller) WriteAsset(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.Assets, name), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *ShellInstaller) Run(t *testing.T, arguments ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", append([]string{f.script, "--bin-dir", f.BinDir}, arguments...)...)
	for key, value := range f.env {
		command.Env = append(command.Env, key+"="+value)
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

func (f *ShellInstaller) URLs(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.env["INSTALLER_TEST_URLS"])
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

func (f *ShellInstaller) Destination() string { return filepath.Join(f.BinDir, "commander") }

func (f *ShellInstaller) Executed() bool {
	_, err := os.Stat(f.env["INSTALLER_TEST_EXECUTED"])
	return err == nil
}
