package playground_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/playground/internal/local"
	fixtures "github.com/defos-ai/commander/apps/playground/tests/fixtures"
	"github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
)

const localSecretValue = "all-in-one-private-secret-value"

func testAllInOneSecrets(t *testing.T) {
	var previousKeyID string
	for _, test := range []struct {
		name, key string
		unset     bool
	}{
		{name: "unset key", unset: true},
		{name: "empty key"},
		{name: "explicit key", key: strings.Repeat("ab", 32)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixtures.AllInOneEnvironment(t)
			t.Setenv("SECRETS_ENCRYPTION_KEY", test.key)
			if test.unset {
				if err := os.Unsetenv("SECRETS_ENCRYPTION_KEY"); err != nil {
					t.Fatal(err)
				}
			}
			session, stop := fixtures.StartLocalAdminSession(t, local.Options{})
			assertLocalSecretEnvironment(t, test.key, !test.unset)
			created := localSecretRequest(t, session, http.MethodPost, "/api/v1/secrets",
				`{"name":"GITHUB_TOKEN","value":"`+localSecretValue+`"}`, http.StatusCreated)
			if created.ID == "" || created.Name != "GITHUB_TOKEN" || created.Version != 1 || created.Status != "available" {
				t.Fatal("all-in-one API did not create secret metadata")
			}
			fetched := localSecretRequest(t, session, http.MethodGet, "/api/v1/secrets/GITHUB_TOKEN", "", http.StatusOK)
			if fetched.ID != created.ID || fetched.Version != created.Version || fetched.Status != created.Status {
				t.Fatal("all-in-one API did not return the saved secret metadata")
			}
			store, err := fssecrets.NewSecretFsStore(filepath.Join(session.Directory, "database"))
			if err != nil {
				t.Fatal(err)
			}
			stored, err := store.Get(t.Context(), secretmodel.Key{Name: created.Name})
			if err != nil || stored.Validate() != nil || stored.Metadata.ID != created.ID || stored.Metadata.Version != created.Version {
				t.Fatal("all-in-one API did not persist an encrypted secret with its metadata")
			}
			if test.key == "" {
				if stored.Value.KeyID == previousKeyID {
					t.Fatal("independent all-in-one sessions reused a generated encryption key")
				}
				previousKeyID = stored.Value.KeyID
			} else {
				cipher, err := crypto.NewSecretCipher(test.key)
				if err != nil {
					t.Fatal(err)
				}
				plaintext, err := cipher.Decrypt(stored)
				defer clear(plaintext)
				if err != nil || string(plaintext) != localSecretValue {
					t.Fatal("all-in-one did not encrypt the stored secret with the explicit key")
				}
			}
			assertLocalSecretPrivacy(t, session, test.key)
			stop()
			if _, err := os.Stat(session.Directory); !os.IsNotExist(err) {
				t.Fatal("all-in-one retained secret state after closing")
			}
			assertLocalSecretEnvironment(t, test.key, !test.unset)
		})
	}
	t.Run("invalid key fails before creating state", func(t *testing.T) {
		for _, test := range []struct{ name, key string }{
			{"blank", "   "},
			{"short", "invalid-private-key"},
			{"non-hex", strings.Repeat("g", 64)},
		} {
			t.Run(test.name, func(t *testing.T) {
				temporary := fixtures.AllInOneEnvironment(t)
				t.Setenv("SECRETS_ENCRYPTION_KEY", test.key)
				// Key validation must win even when creating a session directory would fail.
				for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
					t.Setenv(name, filepath.Join(temporary, "unavailable"))
				}
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				session, err := local.StartWithOptions(ctx, local.Options{})
				if session != nil {
					t.Cleanup(func() { _ = session.Close() })
					t.Fatal("invalid encryption key started a local session")
				}
				if !errors.Is(err, crypto.ErrInvalidKey) || ctx.Err() != nil || strings.Contains(err.Error(), test.key) {
					t.Fatal("invalid encryption key did not fail promptly with a redacted key validation error")
				}
				files, err := os.ReadDir(temporary)
				if err != nil || len(files) != 0 {
					t.Fatal("invalid encryption key created runtime state")
				}
				assertLocalSecretEnvironment(t, test.key, true)
			})
		}
	})
}

func localSecretRequest(t *testing.T, session *local.Session, method, path, body string, status int) models.SecretResponse {
	t.Helper()
	request, err := http.NewRequestWithContext(session.Context, method, session.APIURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth(session.APICredentials.Username, session.APICredentials.Password)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 3 * time.Second, Transport: http.DefaultTransport.(*http.Transport).Clone()}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("secret API returned status %d, want %d (read error: %v)", response.StatusCode, status, err)
	}
	if bytes.Contains(data, []byte(localSecretValue)) {
		t.Fatal("secret API returned the private value")
	}
	var metadata models.SecretResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		t.Fatal("secret API did not return metadata-only JSON")
	}
	return metadata
}

func assertLocalSecretEnvironment(t *testing.T, expected string, present bool) {
	t.Helper()
	if value, exists := os.LookupEnv("SECRETS_ENCRYPTION_KEY"); value != expected || exists != present {
		t.Fatal("all-in-one changed the encryption key environment")
	}
}

func assertLocalSecretPrivacy(t *testing.T, session *local.Session, key string) {
	t.Helper()
	private := []string{localSecretValue}
	if key != "" {
		private = append(private, key)
	}
	for _, value := range private {
		if strings.Contains(strings.Join(session.Notices, "\n"), value) {
			t.Fatal("all-in-one notices disclosed a secret or encryption key")
		}
	}
	root, err := os.OpenRoot(session.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	files := root.FS()
	err = fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		// Empty files cannot contain private data; reading an empty provider
		// lock file would violate its mandatory byte-range lock on Windows.
		if info.Size() == 0 {
			return nil
		}
		// Root reads permit Windows delete sharing, preserving atomic replacement
		// while the running hosts continue writing their catalogs and logs.
		data, err := fs.ReadFile(files, path)
		if os.IsNotExist(err) {
			return nil // Providers may finish an atomic file replacement during this scan.
		}
		if err != nil {
			return err
		}
		for _, value := range private {
			if bytes.Contains(data, []byte(value)) {
				t.Fatal("all-in-one wrote a secret or encryption key to a session file or log")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
