//go:build linux && cgo

package app

/*
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <fcntl.h>
#include <linux/fs.h>
#include <errno.h>

static int lj_rename_noreplace(const char *oldpath, const char *newpath) {
	if (renameat2(AT_FDCWD, oldpath, AT_FDCWD, newpath, RENAME_NOREPLACE) == 0) {
		return 0;
	}
	return errno;
}
*/
import "C"

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

func renameNoReplace(src, dst string) error {
	cSrc := C.CString(src)
	defer C.free(unsafe.Pointer(cSrc))
	cDst := C.CString(dst)
	defer C.free(unsafe.Pointer(cDst))

	ret := C.lj_rename_noreplace(cSrc, cDst)
	if ret == 0 {
		return nil
	}
	errno := syscall.Errno(ret)
	if errors.Is(errno, syscall.EEXIST) {
		return os.ErrExist
	}
	if errors.Is(errno, syscall.EINVAL) || errors.Is(errno, syscall.ENOSYS) || errors.Is(errno, syscall.ENOTSUP) {
		if err := os.Link(src, dst); err != nil {
			return err
		}
		return os.Remove(src)
	}
	return &os.LinkError{Op: "renameat2", Old: src, New: dst, Err: errno}
}
