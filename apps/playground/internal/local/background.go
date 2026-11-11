package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/state"
)

func StartBackground(ctx context.Context, directory string) (Instance, error) {
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	profile, err := state.OpenSharedProfile(directory)
	if err != nil {
		return Instance{}, err
	}
	defer profile.Close()
	root, err := os.OpenRoot(profile.Directory)
	if err != nil {
		return Instance{}, err
	}
	defer root.Close()
	launch, err := state.LockFile(root, ".start.lock")
	if err != nil {
		return Instance{}, fmt.Errorf("another Commander start may be in progress: %w", err)
	}
	defer launch.Close()
	localDirectory := filepath.Join(profile.Directory, "local")
	if instance, err := Discover(ctx, localDirectory); err == nil {
		return instance, nil
	} else if !errors.Is(err, ErrNotRunning) {
		return Instance{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return Instance{}, err
	}
	log, err := openStartupLog(root)
	if err != nil {
		return Instance{}, err
	}
	defer log.Close()
	args := []string{"--all-in-one", "--headless", "--state-dir", profile.Directory, "--temporary=false"}
	command, err := startDetached(executable, args, log)
	if err != nil {
		return Instance{}, fmt.Errorf("start Commander process: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
		close(done)
	}()
	instance, err := awaitBackground(ctx, localDirectory, command.Process.Pid, done)
	if err != nil {
		// Only the child created by this call is eligible for startup cleanup.
		select {
		case <-done:
		default:
			_ = command.Process.Kill()
			<-done
		}
		return Instance{}, fmt.Errorf("%w; see %s", err, filepath.Join(profile.Directory, "startup.log"))
	}
	return instance, nil
}

func awaitBackground(ctx context.Context, directory string, pid int, done <-chan error) (Instance, error) {
	startup, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return Instance{}, fmt.Errorf("Commander exited before readiness: %v", err)
		case <-startup.Done():
			return Instance{}, fmt.Errorf("waiting for Commander readiness: %w", startup.Err())
		case <-tick.C:
			instance, err := Discover(startup, directory)
			if err == nil && instance.PID == pid {
				return instance, nil
			}
			if err != nil && !errors.Is(err, ErrNotRunning) {
				return Instance{}, err
			}
		}
	}
}

func openStartupLog(root *os.Root) (*os.File, error) {
	const name = "startup.log"
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0600)
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("startup.log must be a private regular file")
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) {
		file.Close()
		return nil, errors.New("startup.log changed while opening")
	}
	return file, nil
}
