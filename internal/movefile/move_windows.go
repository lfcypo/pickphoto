//go:build windows

package movefile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func Move(source, target string) error {
	info, samePath, err := prepareMove(source, target)
	if err != nil || samePath {
		return err
	}
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	err = retryBusy(func() error { return windows.MoveFile(from, to) })
	if err == nil {
		return nil
	}
	if os.IsExist(err) {
		return ErrConflict
	}
	if errors.Is(err, windows.ERROR_NOT_SAME_DEVICE) {
		return copyAcrossVolumes(source, target, info)
	}
	return &os.PathError{Op: "move", Path: source, Err: err}
}

func IsBusy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
