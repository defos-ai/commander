//go:build linux

package packages_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes/native"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	"golang.org/x/sys/unix"
)

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
			resolver := fixtures.ProcessResolver(binary, "process-agent-"+mode)
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
			ctx, requestID, _ := fixtures.SpawnCheckpoints(t)
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if _, err := manager.Spawn(ctx, requestID, fixtures.Reference); err == nil {
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
			if mode == "wait" && !fixtures.ProcessRunning(t, pid) {
				t.Fatal("descendant exited before the shutdown scenario")
			}
			if err := manager.Close(); err != nil {
				t.Fatal(err)
			}
			if fixtures.ProcessRunning(t, pid) {
				t.Fatal("spawner reported successful shutdown with a live descendant")
			}
		})
	}
}

func init() { fakes.RunProcessFixture() }
