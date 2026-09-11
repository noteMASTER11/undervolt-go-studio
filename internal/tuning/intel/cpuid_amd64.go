package intel

import (
	"runtime"

	"golang.org/x/sys/unix"
)

func cpuid(eax, ecx uint32) (a, b, c, d uint32)

func CPUIDOnCPU(cpu int, eax, ecx uint32) (a, b, c, d uint32, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var previous unix.CPUSet
	if err = unix.SchedGetaffinity(0, &previous); err != nil {
		return
	}
	var selected unix.CPUSet
	selected.Set(cpu)
	if err = unix.SchedSetaffinity(0, &selected); err != nil {
		return
	}
	defer func() {
		if restoreErr := unix.SchedSetaffinity(0, &previous); err == nil && restoreErr != nil {
			err = restoreErr
		}
	}()

	a, b, c, d = cpuid(eax, ecx)
	return
}
