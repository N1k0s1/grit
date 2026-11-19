//go:build windows

package activity

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows: GetLastInputInfo gives the tick count of the last input event;
// GetTickCount gives the tick count now. Both are 32-bit milliseconds since
// boot, so unsigned subtraction handles the 49.7 day wraparound.

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procLastInput    = user32.NewProc("GetLastInputInfo")
	procGetTickCount = kernel32.NewProc("GetTickCount")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

func idle() (time.Duration, string, error) {
	if procLastInput.Find() != nil || procGetTickCount.Find() != nil {
		return 0, "", ErrUnknown
	}
	info := lastInputInfo{cbSize: uint32(unsafe.Sizeof(lastInputInfo{}))}
	r, _, _ := procLastInput.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, "", ErrUnknown
	}
	now, _, _ := procGetTickCount.Call()
	ms := uint32(now) - info.dwTime
	return time.Duration(ms) * time.Millisecond, "getlastinputinfo", nil
}
