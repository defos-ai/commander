package playground_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/state"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
)

func TestProfileRetainsSettingsAndLockFile(t *testing.T) {
	directory := fixtures.ProfileDirectory(t)
	profile := fixtures.OpenProfile(t, directory)
	settings := fixtures.ProfileSettings()
	before, err := os.Stat(filepath.Join(directory, ".lock"))
	if err != nil {
		t.Fatal(err)
	}

	if err := profile.Save(settings); err != nil {
		t.Fatal(err)
	}
	if err := profile.Close(); err != nil {
		t.Fatal(err)
	}
	profile = fixtures.OpenProfile(t, directory)
	loaded, err := profile.Load()

	if err != nil || loaded != settings {
		t.Fatal("saved settings did not survive reopening the profile", err)
	}
	after, err := os.Stat(filepath.Join(directory, ".lock"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("profile replaced its lock file", err)
	}
	if profile.Directory != directory {
		t.Fatal("profile lost its owned directory")
	}
	if runtime.GOOS != "windows" {
		for name, mode := range map[string]os.FileMode{"": 0700, ".lock": 0600, "profile.json": 0600, "settings.json": 0600} {
			info, err := os.Stat(filepath.Join(directory, name))
			if err != nil || info.Mode().Perm() != mode {
				t.Fatalf("profile permissions differ for %s: %v", name, err)
			}
		}
	}
}

func TestProfileExcludesAnotherOwner(t *testing.T) {
	directory := fixtures.ProfileDirectory(t)
	owner := fixtures.OpenProfile(t, directory)

	other, err := state.Open(directory)

	if other != nil {
		other.Close()
		t.Fatal("a second profile owner acquired the same directory")
	}
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatal("second owner did not receive a profile-in-use error")
	}
	if err := owner.Save(fixtures.ProfileSettings()); err != nil {
		t.Fatal("failed second owner affected the first", err)
	}
}

func TestProfileExcludesAnotherProcess(t *testing.T) {
	if directory := os.Getenv("COMMANDER_TEST_PROFILE_LOCK"); directory != "" {
		profile, err := state.Open(directory)
		if profile != nil {
			profile.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "already in use") {
			t.Fatal("independent process did not reject the active owner")
		}
		return
	}
	directory := fixtures.ProfileDirectory(t)
	fixtures.OpenProfile(t, directory)
	probe := fixtures.ProfileLockProbe(directory)

	output, err := probe.CombinedOutput()

	if err != nil {
		t.Fatalf("independent owner check failed: %v\n%s", err, output)
	}
}

func TestProfileRejectsForeignOrInvalidState(t *testing.T) {
	for _, test := range []struct{ name, file, content string }{
		{"foreign directory", "notes.txt", "keep this file"},
		{"invalid marker", "profile.json", "{"},
		{"different application", "profile.json", `{"application":"other","version":1}`},
		{"newer version", "profile.json", `{"application":"commander","version":2}`},
		{"corrupt settings", "settings.json", "{"},
		{"unknown settings", "settings.json", `{"api_key":"must not be accepted"}`},
		{"null settings", "settings.json", "null"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := fixtures.ProfileDirectory(t)
			if test.file == "notes.txt" {
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				profile := fixtures.OpenProfile(t, directory)
				profile.Close()
			}
			fixtures.ProfileFile(t, directory, test.file, []byte(test.content))

			profile, err := state.Open(directory)

			if profile != nil {
				profile.Close()
			}
			if err == nil {
				t.Fatal("unsafe state was accepted")
			}
			data, err := os.ReadFile(filepath.Join(directory, test.file))
			if err != nil || string(data) != test.content {
				t.Fatal("refused state was changed", err)
			}
		})
	}
}

func TestProfileRejectsSymbolicLinks(t *testing.T) {
	for _, name := range []string{"profile", "profile.json", "settings.json", ".lock", "provider-key.key", "provider-keys.json", "histories"} {
		t.Run(name, func(t *testing.T) {
			directory := fixtures.ProfileDirectory(t)
			target := filepath.Join(t.TempDir(), "target")
			if name == "profile" || name == "histories" {
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				fixtures.ProfileFile(t, filepath.Dir(target), filepath.Base(target), []byte("untouched"))
			}
			link := directory
			if name != "profile" {
				profile := fixtures.OpenProfile(t, directory)
				profile.Close()
				link = filepath.Join(directory, name)
				if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, link); err != nil {
				if runtime.GOOS == "windows" {
					t.Skip("symbolic links require permission on Windows")
				}
				t.Fatal(err)
			}

			profile, err := state.Open(directory)

			if profile != nil {
				profile.Close()
			}
			if err == nil {
				t.Fatal("profile followed a symbolic link")
			}
		})
	}
}

func TestProfileSettingsExcludeCredentialsInURLs(t *testing.T) {
	profile := fixtures.OpenProfile(t, fixtures.ProfileDirectory(t))

	err := profile.Save(state.Settings{BaseURL: "https://user:password@provider.example/v1"})

	if err == nil {
		t.Fatal("profile accepted credentials inside a saved endpoint")
	}
}

func TestProfileKeysAreEncryptedAndBoundToProviderEndpoint(t *testing.T) {
	directory := fixtures.ProfileDirectory(t)
	profile := fixtures.OpenProfile(t, directory)
	const value = "fixture-key-never-store-as-plaintext"
	const endpoint = "https://provider.example/v1"

	if err := profile.SaveKey("openai", endpoint, value); err != nil {
		t.Fatal(err)
	}
	profile.Close()
	profile = fixtures.OpenProfile(t, directory)
	loaded, err := profile.LoadKey("openai", endpoint)

	if err != nil || loaded != value {
		t.Fatal("provider key did not survive reopening", err)
	}
	for _, pair := range [][2]string{{"other", endpoint}, {"openai", endpoint + "/other"}} {
		loaded, err := profile.LoadKey(pair[0], pair[1])
		if err != nil || loaded != "" {
			t.Fatal("key was returned for another provider or endpoint", err)
		}
	}
	for _, name := range []string{"provider-key.key", "provider-keys.json", "profile.json"} {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || bytes.Contains(data, []byte(value)) {
			t.Fatal("provider key was stored as plaintext", err)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(filepath.Join(directory, name))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("provider key files are not private", err)
			}
		}
	}
}

func TestProfileNeverReplacesMissingOrInvalidCacheKey(t *testing.T) {
	for _, test := range []struct{ name, master string }{
		{"missing", ""},
		{"invalid", "invalid-key"},
		{"wrong", strings.Repeat("0", 64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := fixtures.ProfileDirectory(t)
			profile := fixtures.OpenProfile(t, directory)
			if err := profile.SaveKey("openai", "https://provider.example/v1", "fixture-key"); err != nil {
				t.Fatal(err)
			}
			profile.Close()
			before, err := os.ReadFile(filepath.Join(directory, "provider-keys.json"))
			if err != nil {
				t.Fatal(err)
			}
			if test.name == "missing" {
				if err := os.Remove(filepath.Join(directory, "provider-key.key")); err != nil {
					t.Fatal(err)
				}
			} else {
				fixtures.ProfileFile(t, directory, "provider-key.key", []byte(test.master))
			}

			profile, err = state.Open(directory)

			if profile != nil {
				profile.Close()
			}
			if err == nil {
				t.Fatal("profile accepted unrecoverable key cache")
			}
			after, err := os.ReadFile(filepath.Join(directory, "provider-keys.json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("profile replaced retained ciphertext", err)
			}
			_, err = os.Stat(filepath.Join(directory, "provider-key.key"))
			if test.name == "missing" && !os.IsNotExist(err) {
				t.Fatal("profile generated a replacement encryption key")
			}
		})
	}
}

func TestProfileScopesConversationsAndRetainsInvalidData(t *testing.T) {
	directory := fixtures.ProfileDirectory(t)
	profile := fixtures.OpenProfile(t, directory)
	want := json.RawMessage(`{"version":1,"messages":[{"role":"user","content":"hello"}]}`)

	if err := profile.SaveHistory("local", want); err != nil {
		t.Fatal(err)
	}
	loaded, err := profile.LoadHistory("local")

	if err != nil || !bytes.Equal(loaded, want) {
		t.Fatal("saved conversation was not retained", err)
	}
	other, err := profile.LoadHistory("remote:https://remote.example/mcp")
	if err != nil || other != nil {
		t.Fatal("local conversation leaked into another destination", err)
	}
	files, err := os.ReadDir(filepath.Join(directory, "histories"))
	if err != nil || len(files) != 1 {
		t.Fatal("history was not stored within its owned directory", err)
	}
	fixtures.ProfileFile(t, filepath.Join(directory, "histories"), files[0].Name(), []byte("{"))
	if err := profile.SaveHistory("local", want); err == nil {
		t.Fatal("saving history overwrote corrupt data")
	}
	retained, err := os.ReadFile(filepath.Join(directory, "histories", files[0].Name()))
	if err != nil || string(retained) != "{" {
		t.Fatal("corrupt history was changed", err)
	}
}
