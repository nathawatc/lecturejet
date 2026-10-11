//go:build linux

package app

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func renameNoReplace(src, dst string) error {
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EEXIST) {
		return os.ErrExist
	}
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.ENOTSUP) {
		if err := os.Link(src, dst); err != nil {
			return err
		}
		return os.Remove(src)
	}
	return &os.LinkError{Op: "renameat2", Old: src, New: dst, Err: err}
}
