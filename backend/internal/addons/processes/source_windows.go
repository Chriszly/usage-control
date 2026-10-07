package processes

import (
	"errors"
	"fmt"
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
	// Windows keeps no count of the machine's CPU time, so it is added up from
	// the idle time and the processes', in their 100 ns units.
	var sum cpuSum
	return func(now time.Time) (Sample, error) {
		var processes []Process
		var idle uint64
		var err error
		processes, idle, buf, err = ntProcesses(buf)
		if err != nil {
			return Sample{}, err
		}
		// A process's start is a FILETIME, 100 ns units since 1601.
		ft := windows.NsecToFiletime(now.UnixNano())
		at := uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
		return Sample{Processes: processes, Total: sum.add(at, idle, processes), Time: at}, nil
	}
}

// ntProcesses lists every process and the CPUs' idle time, reusing buf,
// which it returns grown when it was too small, or returns why the call
// failed.
func ntProcesses(buf []byte) ([]Process, uint64, []byte, error) {
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
			return nil, 0, buf, fmt.Errorf("NtQuerySystemInformation: %w", err)
		}
		processes, idle := parseNT(buf)
		if len(processes) == 0 {
			return nil, 0, buf, errors.New("NtQuerySystemInformation listed no processes")
		}
		return processes, idle, buf, nil
	}
	return nil, 0, buf, errors.New("NtQuerySystemInformation: more processes started at every try")
}

// parseNT walks the list NtQuerySystemInformation wrote to buf, and returns
// its processes and the CPUs' idle time.
func parseNT(buf []byte) ([]Process, uint64) {
	var processes []Process
	var idle uint64
	for offset := 0; offset+int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{})) <= len(buf); {
		// Each entry starts at an offset Windows gave, within buf.
		info := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[offset])) //nolint:gosec // see above
		pid := int(info.UniqueProcessID)
		cpu := uint64(info.UserTime) + uint64(info.KernelTime) //nolint:gosec // times are not negative
		// PID 0 is the idle time of the CPUs, not a process.
		if pid == 0 {
			idle = cpu
		} else {
			processes = append(processes, Process{
				PID:    pid,
				Start:  uint64(info.CreateTime), //nolint:gosec // a time after 1601
				Name:   info.ImageName.String(),
				CPU:    cpu,
				Memory: uint64(info.WorkingSetSize),
			})
		}
		if info.NextEntryOffset == 0 {
			break
		}
		offset += int(info.NextEntryOffset)
	}
	return processes, idle
}
