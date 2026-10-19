//go:build linux

package packages_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/native"
	"github.com/defos-ai/commander/framework/id"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	"golang.org/x/sys/unix"
)

// A downloaded copy of the test executable provides a deterministic subprocess
// fixture without shell dependencies or public release downloads.
func init() {
	name := filepath.Base(os.Args[0])
	if name != "process-agent-exit" && name != "process-agent-wait" {
		return
	}
	go func() {
		time.Sleep(15 * time.Second)
		os.Exit(1)
	}()
	storage := os.Getenv("STORAGE_URL")
	if len(os.Args) == 2 && os.Args[1] == "descendant" {
		signal.Ignore(syscall.SIGTERM)
		if err := os.WriteFile(filepath.Join(storage, "descendant.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(1)
		}
		select {}
	}
	if err := os.MkdirAll(storage, 0700); err != nil {
		os.Exit(1)
	}
	termination := make(chan os.Signal, 1)
	signal.Notify(termination, syscall.SIGTERM)
	child := exec.Command(os.Args[0], "descendant")
	child.Env = os.Environ()
	if err := child.Start(); err != nil {
		os.Exit(1)
	}
	for {
		if _, err := os.Stat(filepath.Join(storage, "descendant.pid")); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if name == "process-agent-wait" {
		<-termination
	}
	// Deliberately exit before our child: the spawner must kill descendants
	// even when its direct process is already gone or exits first on SIGTERM.
	os.Exit(0)
}

func TestNativeSpawnerStopsDescendantsAfterTheirParentExits(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"exit", "wait"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = bus.Close() })
			resolver := processFixtureResolver(binary, "process-agent-"+mode)
			t.Cleanup(resolver.CloseIdleConnections)
			storage := filepath.Join(root, "storage")
			manager, err := native.NewManager(t.Context(), native.Config{
				Directory: filepath.Join(root, "spawner"), Bus: bus, BusURL: filepath.Join(root, "bus"),
				StorageURL: storage, Resolver: resolver, RegistrationTimeout: 2 * time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := manager.Close(); err != nil {
					t.Error(err)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if _, err := manager.Spawn(ctx, "task:"+id.TaskID(), fixtureReference); err == nil {
				t.Fatal("non-registering fixture unexpectedly registered")
			}
			data, err := os.ReadFile(filepath.Join(storage, "descendant.pid"))
			if err != nil {
				t.Fatalf("fixture did not start its descendant: %v", err)
			}
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			pidfd, openErr := unix.PidfdOpen(pid, 0)
			if openErr == nil {
				t.Cleanup(func() {
					_ = unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
					if ready, err := unix.Poll([]unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}, 2000); err != nil || ready == 0 {
						t.Errorf("fixture descendant did not stop: %v", err)
					}
					_ = unix.Close(pidfd)
				})
			} else if !errors.Is(openErr, unix.ESRCH) {
				t.Fatal(openErr)
			}
			if mode == "wait" && !processFixtureRunning(t, pid) {
				t.Fatal("descendant exited before the shutdown scenario")
			}
			if err := manager.Close(); err != nil {
				t.Fatal(err)
			}
			if processFixtureRunning(t, pid) {
				t.Fatal("spawner reported successful shutdown with a live descendant")
			}
		})
	}
}

func processFixtureResolver(binary []byte, asset string) *packages.Resolver {
	checksum := sha256.Sum256(binary)
	manifest := fmt.Sprintf("schema_version: 1\ntype: fixture-agent\nversion: v0.1.0\nnative:\n  %s/%s:\n    asset: %s\n    sha256: %q\n", runtime.GOOS, runtime.GOARCH, asset, fmt.Sprintf("%x", checksum))
	return packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		if filepath.Base(request.URL.Path) == "agent.yaml" {
			return response(request, http.StatusOK, manifest), nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(bytes.NewReader(binary)), Request: request}, nil
	}))
}

func processFixtureRunning(t *testing.T, pid int) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(data[strings.LastIndexByte(string(data), ')')+1:]))
	if len(fields) == 0 {
		t.Fatal("cannot inspect fixture process state")
	}
	return fields[0] != "Z" && fields[0] != "X"
}
