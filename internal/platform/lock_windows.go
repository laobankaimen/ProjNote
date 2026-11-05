//go:build windows

package platform

import (
	"golang.org/x/sys/windows"
	"os"
)

type Lock struct {
	file    *os.File
	overlap windows.Overlapped
}

func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	l := &Lock{file: f}
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &l.overlap); err != nil {
		f.Close()
		return nil, err
	}
	return l, nil
}
func (l *Lock) Close() error {
	_ = windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlap)
	return l.file.Close()
}
