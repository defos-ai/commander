package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

// Inject permission failures inside the transaction, after it pins the directory
// and takes the lock. This exercises publication without timing races or hooks in
// production code; ordinary store behavior is covered through its public API.
func TestSecretFsStorePublicationFailures(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions and an unprivileged user")
	}
	for _, test := range []struct {
		name      string
		mode      os.FileMode
		uncertain bool
		unchanged bool
	}{
		{"before publication", 0500, false, false},
		{"after publication", 0300, true, false},
		{"identical retry sync", 0300, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			restore := func() {
				t.Helper()
				if err := os.Chmod(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(restore)
			store, err := NewSecretFsStore(root)
			if err != nil {
				t.Fatal(err)
			}
			cipher, err := crypto.NewSecretCipher(strings.Repeat("ab", 32))
			if err != nil {
				t.Fatal(err)
			}
			stamp := time.Date(2026, 10, 16, 12, 0, 0, 123456789, time.UTC)
			metadata := secretmodel.Metadata{
				ID: id.New(), Key: secretmodel.Key{Scope: secretmodel.Scope{AgentType: "builder-agent"}, Name: "TOKEN"},
				Version: 1, CreatedAt: stamp, UpdatedAt: stamp,
			}
			initial, err := cipher.Encrypt(metadata, []byte("initial-test-token"))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Create(t.Context(), initial); err != nil {
				t.Fatal(err)
			}
			wanted := initial
			if !test.unchanged {
				metadata.Version++
				metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Nanosecond)
				wanted, err = cipher.Encrypt(metadata, []byte("replacement-test-token"))
				if err != nil {
					t.Fatal(err)
				}
			}
			err = store.update(t.Context(), func([]storedRecord) ([]storedRecord, error) {
				if err := os.Chmod(root, test.mode); err != nil {
					t.Fatal(err)
				}
				return []storedRecord{{Secret: &wanted}}, nil
			})
			restore()
			if !errors.Is(err, os.ErrPermission) || errors.Is(err, secretmodel.ErrWriteUncertain) != test.uncertain {
				t.Fatalf("write must preserve the permission error and publication outcome: %v", err)
			}
			reopened, err := NewSecretFsStore(root)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := reopened.Get(t.Context(), initial.Metadata.Key)
			expected := initial
			if test.uncertain {
				expected = wanted
			}
			if err != nil || !equalSecrets(saved, expected) {
				t.Fatalf("reopened state did not match publication outcome: %v", err)
			}
			if test.unchanged {
				err = reopened.Create(t.Context(), wanted)
			} else {
				err = reopened.Replace(t.Context(), wanted, 1)
			}
			if err != nil {
				t.Fatalf("retry after permissions recovered: %v", err)
			}
			info, err := os.Stat(filepath.Join(root, "secrets.json"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatal("secret collection must remain owner-only")
			}
		})
	}
}
