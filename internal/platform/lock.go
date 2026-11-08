package platform

import (
	"context"
	"fmt"
	"os"
)

type FileLock struct {
	file *os.File
	path string
}

func AcquireFileLock(_ context.Context, path string) (*FileLock, error) {
	file, err := openLockedFile(path)
	if err != nil {
		return nil, err
	}
	if err := file.Truncate(0); err == nil {
		_, _ = file.WriteString(fmt.Sprintf("pid=%d\n", os.Getpid()))
	}
	return &FileLock{file: file, path: path}, nil
}

func (l *FileLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func (l *FileLock) Close() error {
	return l.Release()
}
