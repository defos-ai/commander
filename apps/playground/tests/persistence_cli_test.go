package playground_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/playground/cli"
	"github.com/defos-ai/commander/apps/playground/internal/state"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestPlaygroundLoadsSavedSettings(t *testing.T) {
	fixtures.PlaygroundEnvironment(t)
	directory := filepath.Join(t.TempDir(), "profile")
	profile, err := state.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.Save(state.Settings{Provider: "other", Model: "fixture", BaseURL: "http://localhost:1234/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := profile.Close(); err != nil {
		t.Fatal(err)
	}

	playground := fixtures.StartPlayground(t, []string{"--state-dir=" + directory})

	view := playground.Wait("Other (OpenAI-compatible) API key")
	if !strings.Contains(view, "Remembered encrypted on this machine") {
		t.Fatal("default remote Playground did not enable persistent provider setup")
	}
}

func TestPlaygroundExplicitProviderOverridesSavedSettings(t *testing.T) {
	fixtures.PlaygroundEnvironment(t)
	directory := filepath.Join(t.TempDir(), "profile")
	profile, err := state.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.Save(state.Settings{Provider: "other", Model: "fixture", BaseURL: "http://localhost:1234/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := profile.Close(); err != nil {
		t.Fatal(err)
	}

	playground := fixtures.StartPlayground(t, []string{"--state-dir=" + directory, "--provider=openai"})

	view := playground.Wait("OpenAI API key")
	if strings.Contains(view, "Other (OpenAI-compatible) API key") || !strings.Contains(view, "OPENAI_API_KEY") {
		t.Fatal("saved provider settings overrode explicit setup")
	}
}

func TestTemporaryPlaygroundLeavesUnknownDirectoryAlone(t *testing.T) {
	fixtures.PlaygroundEnvironment(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "existing.txt")
	if err := os.WriteFile(path, []byte("keep this file"), 0600); err != nil {
		t.Fatal(err)
	}

	playground := fixtures.StartPlayground(t, []string{"--temporary", "--state-dir=" + directory, "--provider=other", "--base-url=http://localhost:1234/v1"})
	view := playground.Wait("Other (OpenAI-compatible) API key")

	if !strings.Contains(view, "Kept in memory for this session") || strings.Contains(view, "Remembered encrypted") {
		t.Fatal("temporary Playground enabled saved keys")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "existing.txt" {
		t.Fatal("temporary Playground modified its configured state directory")
	}
}

func TestPlaygroundRefusesUnknownState(t *testing.T) {
	fixtures.PlaygroundEnvironment(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "existing.txt")
	if err := os.WriteFile(path, []byte("keep this file"), 0600); err != nil {
		t.Fatal(err)
	}

	err := cli.Run(t.Context(), []string{"--state-dir=" + directory}, strings.NewReader(""), io.Discard, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "--temporary") {
		t.Fatal("unrecognized state did not explain the temporary alternative")
	}
	entries, readErr := os.ReadDir(directory)
	data, fileErr := os.ReadFile(path)
	if readErr != nil || fileErr != nil || len(entries) != 1 || string(data) != "keep this file" {
		t.Fatal("startup modified an unrecognized state directory")
	}
}
