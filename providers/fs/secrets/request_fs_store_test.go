package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

func TestRequestFsStorePublicationFailures(t *testing.T) {
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
			store, err := NewRequestFsStore(root)
			if err != nil {
				t.Fatal(err)
			}
			stamp := time.Date(2026, 10, 17, 12, 0, 0, 123456789, time.UTC)
			request := secretmodel.Request{
				ID: id.New(), Key: secretmodel.Key{Name: "TOKEN"},
				AgentID: "builder-agent-12345678", AgentType: "builder-agent", Status: secretmodel.Pending,
				RequestedAt: stamp, ExpiresAt: stamp.Add(time.Hour),
			}
			if err := store.Create(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			// Change permissions only after the transaction acquires its root and
			// lock; this injects a deterministic publication/sync failure.
			err = store.update(t.Context(), func(records []secretmodel.RequestRecord) ([]secretmodel.RequestRecord, error) {
				if err := os.Chmod(root, test.mode); err != nil {
					t.Fatal(err)
				}
				if test.unchanged {
					return records, nil
				}
				updated, err := records[0].Cancel(stamp.Add(time.Second))
				if err != nil {
					return nil, err
				}
				return []secretmodel.RequestRecord{updated}, nil
			})
			restore()
			if !errors.Is(err, os.ErrPermission) || errors.Is(err, secretmodel.ErrWriteUncertain) != test.uncertain {
				t.Fatalf("publication lost permission cause or certainty: %v", err)
			}
			reopened, err := NewRequestFsStore(root)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := reopened.Get(t.Context(), request.ID)
			want := secretmodel.Pending
			if test.uncertain && !test.unchanged {
				want = secretmodel.Cancelled
			}
			if err != nil || saved.Request.Status != want {
				t.Fatal("reopened request state does not match publication outcome")
			}
			if test.unchanged {
				err = reopened.Create(t.Context(), request)
			} else {
				err = reopened.Cancel(t.Context(), request.ID, stamp.Add(time.Second))
			}
			if err != nil {
				t.Fatalf("retry after restoring permissions: %v", err)
			}
			info, err := os.Stat(filepath.Join(root, "secret-requests.json"))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("request collection permissions must remain owner-only")
			}
		})
	}
}
