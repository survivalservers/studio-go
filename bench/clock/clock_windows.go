//go:build windows

package clock

import (
	"syscall"
	"time"
	"unsafe"
)

const source = "QueryPerformanceCounter"

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll") // a Known DLL: always loaded from the system folder
	qpc      = kernel32.NewProc("QueryPerformanceCounter")
	freq     int64 // counts a second; fixed at boot
)

func init() {
	if r, _, err := kernel32.NewProc("QueryPerformanceFrequency").Call(uintptr(unsafe.Pointer(&freq))); r == 0 || freq <= 0 {
		panic("clock: QueryPerformanceFrequency failed: " + err.Error())
	}
}

func now() time.Duration {
	var c int64
	qpc.Call(uintptr(unsafe.Pointer(&c)))
	// Whole seconds and the remainder separately: counts x 1e9 would overflow after a few minutes of uptime.
	return time.Duration(c/freq)*time.Second + time.Duration(c%freq*int64(time.Second)/freq)
}
