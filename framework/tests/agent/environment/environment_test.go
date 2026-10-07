package environment_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/defos-ai/commander/framework/agent/environment"
)

func TestFindExecutablePermissions(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0700} {
		t.Run(mode.String(), func(t *testing.T) {
			directory := t.TempDir()
			filename := filepath.Join(directory, "scanner.exe")
			if err := os.WriteFile(filename, []byte("fixture, never executed"), mode); err != nil {
				t.Fatal(err)
			}
			local := &environment.Local{SearchPaths: []string{directory}}
			got, err := local.FindExecutable(t.Context(), "scanner.exe", nil)
			want := filename
			if runtime.GOOS != "windows" && mode&0111 == 0 {
				want = ""
			}
			if err != nil || got != want {
				t.Fatalf("FindExecutable = %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestFindExecutableWindowsExtensions(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable extensions")
	}
	directory := t.TempDir()
	filename := filepath.Join(directory, "scanner.exe")
	if err := os.WriteFile(filename, []byte("fixture, never executed"), 0600); err != nil {
		t.Fatal(err)
	}
	local := &environment.Local{SearchPaths: []string{directory}}
	for _, test := range []struct{ extensions, want string }{
		{".EXE;.CMD", filename},
		{".CMD", ""},
	} {
		t.Run(test.extensions, func(t *testing.T) {
			t.Setenv("PATHEXT", test.extensions)
			got, err := local.FindExecutable(t.Context(), "scanner", nil)
			if err != nil || got != test.want {
				t.Fatalf("FindExecutable = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestFindExecutableRespectsScope(t *testing.T) {
	allowed, outside := t.TempDir(), t.TempDir()
	for _, directory := range []string{allowed, outside} {
		if err := os.WriteFile(filepath.Join(directory, "scanner.exe"), []byte("fixture, never executed"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	local := &environment.Local{SearchPaths: []string{outside}}
	scope := &environment.DiscoveryScope{Paths: []string{allowed}}
	got, err := local.FindExecutable(t.Context(), "scanner.exe", scope)
	want, resolveErr := filepath.EvalSymlinks(filepath.Join(allowed, "scanner.exe"))
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if err != nil || got != want {
		t.Fatalf("scoped discovery = %q, %v; want %q", got, err, want)
	}
	for _, test := range []struct{ name, directory string }{
		{"inside", allowed},
		{"outside", outside},
	} {
		t.Run(test.name, func(t *testing.T) {
			link := filepath.Join(allowed, test.name+".exe")
			target := filepath.Join(test.directory, "scanner.exe")
			if err := os.Symlink(target, link); err != nil {
				if runtime.GOOS == "windows" {
					t.Skipf("symlinks unavailable: %v", err)
				}
				t.Fatal(err)
			}
			got, err := local.FindExecutable(t.Context(), filepath.Base(link), scope)
			expected := want
			if test.directory == outside {
				expected = ""
			}
			if err != nil || got != expected {
				t.Fatalf("scoped symlink = %q, %v; want %q", got, err, expected)
			}
		})
	}
}
