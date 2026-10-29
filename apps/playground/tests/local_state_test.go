package playground_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/local"
	"github.com/defos-ai/commander/apps/playground/tests/fixtures"
	"github.com/defos-ai/commander/framework/crypto"
)

func TestPersistentLocalStateSurvivesStartupFailure(t *testing.T) {
	fixtures.AllInOneEnvironment(t)
	directory := fixtures.LocalStateDirectory(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := local.StartWithOptions(ctx, local.Options{Directory: directory})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted local startup: %v", err)
	}
	credentials := fixtures.ReadLocalStateFile(t, directory, "credentials.json")
	key := fixtures.ReadLocalStateFile(t, directory, "secrets.key")
	fixtures.WriteLocalStateFile(t, directory, "hosts.log", []byte("earlier session\n"))

	_, err = local.StartWithOptions(ctx, local.Options{Directory: directory})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("restart interrupted local startup: %v", err)
	}
	if !bytes.Equal(credentials, fixtures.ReadLocalStateFile(t, directory, "credentials.json")) || !bytes.Equal(key, fixtures.ReadLocalStateFile(t, directory, "secrets.key")) {
		t.Fatal("restart replaced retained credentials or encryption key")
	}
	if !bytes.HasPrefix(fixtures.ReadLocalStateFile(t, directory, "hosts.log"), []byte("earlier session\n")) {
		t.Fatal("restart overwrote the earlier host log")
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{"credentials.json", "secrets.key"} {
			info, err := os.Stat(filepath.Join(directory, name))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("retained local bootstrap is not private")
			}
		}
	}
}

func TestPersistentLocalStateRejectsInvalidBootstrap(t *testing.T) {
	for _, scenario := range []string{"unknown directory", "missing credentials", "missing key", "corrupt credentials", "invalid key", "different valid key"} {
		t.Run(scenario, func(t *testing.T) {
			fixtures.AllInOneEnvironment(t)
			directory := fixtures.LocalStateDirectory(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if scenario == "unknown directory" {
				fixtures.WriteLocalStateFile(t, directory, "existing.txt", []byte("preserve existing data"))
			} else {
				if _, err := local.StartWithOptions(ctx, local.Options{Directory: directory}); !errors.Is(err, context.Canceled) {
					t.Fatalf("prepare interrupted startup: %v", err)
				}
				switch scenario {
				case "missing credentials":
					fixtures.RemoveLocalStateFile(t, directory, "credentials.json")
				case "missing key":
					fixtures.RemoveLocalStateFile(t, directory, "secrets.key")
				case "corrupt credentials":
					fixtures.WriteLocalStateFile(t, directory, "credentials.json", []byte(`{"private":"do-not-disclose", broken}`))
				case "invalid key":
					fixtures.WriteLocalStateFile(t, directory, "secrets.key", []byte("do-not-disclose"))
				case "different valid key":
					fixtures.WriteLocalStateFile(t, directory, "secrets.key", []byte(strings.Repeat("cd", 32)))
				}
			}

			_, err := local.StartWithOptions(ctx, local.Options{Directory: directory})

			if err == nil || errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "do-not-disclose") {
				t.Fatalf("invalid saved bootstrap did not fail safely: %v", err)
			}
			if _, err := os.Stat(directory); err != nil {
				t.Fatal("invalid bootstrap startup removed retained state")
			}
			if scenario == "unknown directory" && string(fixtures.ReadLocalStateFile(t, directory, "existing.txt")) != "preserve existing data" {
				t.Fatal("unknown directory contents changed")
			}
		})
	}
}

func TestPersistentLocalStateRejectsConflictingOverrides(t *testing.T) {
	for _, name := range []string{"API_USERNAME", "API_PASSWORD", "MCP_TOKEN", "SECRETS_ENCRYPTION_KEY"} {
		t.Run(name, func(t *testing.T) {
			fixtures.AllInOneEnvironment(t)
			directory := fixtures.LocalStateDirectory(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := local.StartWithOptions(ctx, local.Options{Directory: directory}); !errors.Is(err, context.Canceled) {
				t.Fatalf("prepare interrupted startup: %v", err)
			}
			value := "different-private-override"
			if name == "SECRETS_ENCRYPTION_KEY" {
				value = strings.Repeat("cd", 32)
			}
			t.Setenv(name, value)

			_, err := local.StartWithOptions(ctx, local.Options{Directory: directory})

			if err == nil || !strings.Contains(err.Error(), "conflicts") || strings.Contains(err.Error(), value) {
				t.Fatalf("conflicting override did not fail safely: %v", err)
			}
		})
	}
}

func TestPersistentLocalStateValidatesKeyBeforeCreatingDirectory(t *testing.T) {
	fixtures.AllInOneEnvironment(t)
	directory := fixtures.LocalStateDirectory(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", "invalid-private-key")

	_, err := local.StartWithOptions(t.Context(), local.Options{Directory: directory})

	if !errors.Is(err, crypto.ErrInvalidKey) || strings.Contains(err.Error(), "invalid-private-key") {
		t.Fatalf("invalid configured key did not fail safely: %v", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("invalid configured key created local state")
	}
}

func TestSavedLocalCredentialsReadWithoutEnvironmentOrInitialization(t *testing.T) {
	fixtures.AllInOneEnvironment(t)
	directory := fixtures.LocalStateDirectory(t)

	_, _, err := local.SavedCredentials(directory)

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing local bootstrap: %v", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("credential lookup initialized local state")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := local.StartWithOptions(ctx, local.Options{Directory: directory}); !errors.Is(err, context.Canceled) {
		t.Fatalf("prepare interrupted startup: %v", err)
	}
	before := fixtures.ReadLocalStateFile(t, directory, "credentials.json")
	t.Setenv("API_PASSWORD", "conflicting-password")
	t.Setenv("MCP_TOKEN", "conflicting-token")
	t.Setenv("SECRETS_ENCRYPTION_KEY", "invalid-key")

	api, mcp, err := local.SavedCredentials(directory)

	if err != nil || api.Username != "commander" || api.Password != "all-in-one-api-secret" || mcp.Token != "all-in-one-mcp-secret" {
		t.Fatal("credential lookup did not return saved values independently of the environment")
	}
	if !bytes.Equal(before, fixtures.ReadLocalStateFile(t, directory, "credentials.json")) {
		t.Fatal("credential lookup changed saved state")
	}
}

func TestPersistentAllInOneState(t *testing.T) {
	fixtures.AllInOneEnvironment(t)
	for _, name := range []string{"API_USERNAME", "API_PASSWORD", "MCP_TOKEN"} {
		t.Setenv(name, "")
	}
	directory := fixtures.LocalStateDirectory(t)
	first, stopFirst := fixtures.StartLocalAdminSession(t, local.Options{Directory: directory})
	api, mcp := first.APICredentials, first.MCPCredentials
	created := localSecretRequest(t, first, http.MethodPost, "/api/v1/secrets", `{"name":"GITHUB_TOKEN","value":"`+localSecretValue+`"}`, http.StatusCreated)
	stopFirst()
	key := fixtures.ReadLocalStateFile(t, directory, "secrets.key")

	second, stopSecond := fixtures.StartLocalAdminSession(t, local.Options{Directory: directory})
	fetched := localSecretRequest(t, second, http.MethodGet, "/api/v1/secrets/GITHUB_TOKEN", "", http.StatusOK)
	stopSecond()

	if second.APICredentials != api || second.MCPCredentials != mcp || !bytes.Equal(key, fixtures.ReadLocalStateFile(t, directory, "secrets.key")) {
		t.Fatal("restart replaced generated credentials or encryption key")
	}
	if fetched.ID != created.ID || fetched.Version != created.Version || fetched.Status != "available" {
		t.Fatal("restart did not retain the encrypted secret")
	}
	if !strings.Contains(strings.Join(second.Notices, "\n"), "Persistent session") {
		t.Fatal("persistent session was described as temporary")
	}
	for _, name := range []string{"API_USERNAME", "API_PASSWORD", "MCP_TOKEN", "SECRETS_ENCRYPTION_KEY"} {
		if os.Getenv(name) != "" {
			t.Fatal("persistent session changed the process environment")
		}
	}
}
