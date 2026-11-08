//go:build windows

package platform

import (
	"fmt"
	"os"
	"syscall"
)

func openLockedFile(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(
		name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0,
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("acquire session lock: %w", err)
	}
	return os.NewFile(uintptr(handle), path), nil
}

func unlockFile(*os.File) error { return nil }
