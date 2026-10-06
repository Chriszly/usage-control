package processes

import (
	"errors"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NewSource returns where the machine's processes are read: on Windows one
// call, NtQuerySystemInformation, lists every process with its name, CPU time
// and working set, as Task Manager does. Unlike opening each process, it
// needs no rights over the processes, so the Local Service account sees
// them all. The argument, where /proc is, does not apply.
func NewSource(string) Source {
	var buf []byte
	start := time.Now()
	cpus := uint64(runtime.NumCPU()) //nolint:gosec // a count of CPUs is positive
	return func(now time.Time) (Sample, bool) {
		var processes []Process
		processes, buf = ntProcesses(buf)
		if processes == nil {
			return Sample{}, false
		}
		// The machine's CPU time is the time passed on every CPU, in the
		// 100 ns units of the processes' times.
		elapsed := uint64(max(0, now.Sub(start).Nanoseconds()/100))
		return Sample{Processes: processes, Total: elapsed * cpus}, true
	}
}

// ntProcesses lists every process, reusing buf, which it returns grown when
// it was too small. It returns no processes when the call fails.
func ntProcesses(buf []byte) ([]Process, []byte) {
	if len(buf) == 0 {
		buf = make([]byte, 512*1024)
	}
	for range 5 {
		var needed uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation,
			unsafe.Pointer(&buf[0]), uint32(len(buf)), &needed) //nolint:gosec // the buffer stays far below 4 GB
		if errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			// More processes may start before the next try.
			buf = make([]byte, max(int(needed), len(buf))+64*1024)
			continue
		}
		if err != nil {
			return nil, buf
		}
		return parseNT(buf), buf
	}
	return nil, buf
}

// parseNT walks the list NtQuerySystemInformation wrote to buf.
func parseNT(buf []byte) []Process {
	var processes []Process
	for offset := 0; offset+int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{})) <= len(buf); {
		// Each entry starts at an offset Windows gave, within buf.
		info := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[offset])) //nolint:gosec // see above
		pid := int(info.UniqueProcessID)
		// PID 0 is the idle time of the CPUs, not a process.
		if pid != 0 {
			name := info.ImageName.String()
			processes = append(processes, Process{
				PID:    pid,
				Start:  uint64(info.CreateTime), //nolint:gosec // a time after 1601
				Name:   name,
				CPU:    uint64(info.UserTime) + uint64(info.KernelTime), //nolint:gosec // times are not negative
				Memory: uint64(info.WorkingSetSize),
			})
		}
		if info.NextEntryOffset == 0 {
			break
		}
		offset += int(info.NextEntryOffset)
	}
	return processes
}
