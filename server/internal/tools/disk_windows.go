//go:build windows

package tools

import "golang.org/x/sys/windows"

// DiskUsage returns free and total bytes for a folder's drive.
func DiskUsage(path string) (free, total uint64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	err = windows.GetDiskFreeSpaceEx(p, &free, &total, nil)
	return free, total, err
}
