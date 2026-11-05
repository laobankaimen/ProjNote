//go:build windows

package platform

import "golang.org/x/sys/windows"

func AvailableBytes(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	if err = windows.GetDiskFreeSpaceEx(p, &available, &total, &free); err != nil {
		return 0, err
	}
	return available, nil
}
