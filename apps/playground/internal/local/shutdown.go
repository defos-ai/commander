package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/state"
)

var ErrStopRequested = errors.New("local Commander stop requested")

func Stop(ctx context.Context, directory string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	root, err := openExistingStateDirectory(directory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close()
	instance, recordErr := readInstance(root)
	if recordErr != nil && !errors.Is(recordErr, os.ErrNotExist) {
		return false, recordErr
	}
	lease, err := existingBackendLease(root)
	if err == nil {
		return false, lease.Close()
	}
	if errors.Is(err, os.ErrNotExist) && errors.Is(recordErr, os.ErrNotExist) {
		return false, nil
	}
	if !errors.Is(err, state.ErrLocked) {
		return false, fmt.Errorf("inspect local backend ownership: %w", err)
	}
	if recordErr != nil {
		return false, errors.New("local Commander is starting or stopping; retry after its ownership settles")
	}
	if err := requestShutdown(root, instance.ID); err != nil {
		return false, err
	}
	return awaitShutdown(ctx, root, instance.ID)
}

func existingBackendLease(root *os.Root) (*os.File, error) {
	info, err := root.Lstat(".lock")
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("local backend lock must be a private regular file")
	}
	lease, err := state.LockFile(root, ".lock")
	if err != nil {
		return nil, err
	}
	opened, err := lease.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.Join(errors.New("local backend lock changed while opening"), lease.Close())
	}
	return lease, nil
}

func requestShutdown(root *os.Root, instanceID string) error {
	file, err := root.OpenFile("stop-"+instanceID, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		_, err = readStateFile(root, "stop-"+instanceID, 0)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err != nil {
		return err
	}
	return file.Close()
}

func awaitShutdown(ctx context.Context, root *os.Root, instanceID string) (bool, error) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		lease, err := existingBackendLease(root)
		if err == nil {
			err = errors.Join(removeShutdownRequest(root, instanceID), lease.Close())
			return err == nil, err
		}
		if !errors.Is(err, state.ErrLocked) {
			return false, fmt.Errorf("await local backend shutdown: %w", err)
		}
		instance, err := readInstance(root)
		if err == nil && instance.ID != instanceID {
			return true, removeShutdownRequest(root, instanceID)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-tick.C:
		}
	}
}

func (s *Session) startShutdownWatcher() {
	if !s.persistent {
		return
	}
	s.shutdownDone = make(chan struct{})
	go func() {
		defer close(s.shutdownDone)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		invalidReported := false
		for {
			select {
			case <-s.Context.Done():
				return
			case <-tick.C:
			}
			_, err := readStateFile(s.stateRoot, "stop-"+s.InstanceID, 0)
			if errors.Is(err, os.ErrNotExist) {
				invalidReported = false
				continue
			}
			if err != nil {
				if !invalidReported {
					fmt.Fprintf(s.log, "local shutdown request rejected: %v\n", err)
					invalidReported = true
				}
				continue
			}
			s.cancel(ErrStopRequested)
			return
		}
	}()
}

func removeShutdownRequest(root *os.Root, instanceID string) error {
	if _, err := readStateFile(root, "stop-"+instanceID, 0); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := root.Remove("stop-" + instanceID); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
