package jsonfile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func openRootFile(root *os.Root, name string, create bool) (*os.File, error) {
	flags := os.O_RDONLY | windows.FILE_FLAG_OPEN_REPARSE_POINT
	if create {
		flags = os.O_RDWR | windows.FILE_FLAG_OPEN_REPARSE_POINT
	}
	file, err := root.OpenFile(name, flags, 0600)
	if create && errors.Is(err, os.ErrNotExist) {
		file, err = root.OpenFile(name, flags|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			file, err = root.OpenFile(name, flags, 0600)
		}
	}
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info)
	if err == nil && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		err = errors.New("JSON collection and lock paths must not be reparse points")
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func tryLock(file *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

// Windows does not support fsync on directories. The temporary file itself is
// synced before the same-volume replacement.
func syncDirectory(string) error { return nil }

func syncRoot(*os.Root) error { return nil }
