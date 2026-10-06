package processes

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// procFS reads processes from a Linux /proc: /proc/stat for the machine's
// CPU time and /proc/<pid>/stat for each process, which every user may read
// unless /proc hides other users' processes (hidepid).
type procFS struct {
	dir      string
	pageSize uint64
	buf      []byte
}

func newProcFS(dir string, pageSize int) *procFS {
	return &procFS{dir: dir, pageSize: uint64(pageSize), buf: make([]byte, 4096)} //nolint:gosec // a page size is positive
}

// sample reads every process. A process that ends while it is read is left
// out.
func (p *procFS) sample() (Sample, bool) {
	total, ok := parseTotal(p.read(filepath.Join(p.dir, "stat")))
	if !ok {
		return Sample{}, false
	}
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return Sample{}, false
	}
	processes := make([]Process, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if process, ok := parseStat(pid, p.read(filepath.Join(p.dir, entry.Name(), "stat")), p.pageSize); ok {
			processes = append(processes, process)
		}
	}
	return Sample{Processes: processes, Total: total}, true
}

// read returns a short file's text in p.buf, which the next read reuses, or
// nothing when it cannot be read. Its path is made of /proc's own names.
func (p *procFS) read(path string) []byte {
	file, err := os.Open(path) //nolint:gosec // see above
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	n, err := io.ReadFull(file, p.buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return p.buf[:n]
}

// parseTotal reads the CPU time of all CPUs together from /proc/stat's first
// line, "cpu  user nice system idle iowait irq softirq steal guest
// guest_nice", in clock ticks. Guest time is already part of user time.
func parseTotal(text []byte) (uint64, bool) {
	line, _, _ := bytes.Cut(text, []byte("\n"))
	fields := bytes.Fields(line)
	if len(fields) < 9 || string(fields[0]) != "cpu" {
		return 0, false
	}
	var total uint64
	for _, field := range fields[1:9] {
		n, err := strconv.ParseUint(string(field), 10, 64)
		if err != nil {
			return 0, false
		}
		total += n
	}
	return total, true
}

// parseStat reads /proc/<pid>/stat: "pid (comm) state ppid ...". The name
// may hold spaces and parentheses, so the fields start after the last ")".
// Counted from 1 as in proc(5), it needs utime (14), stime (15), starttime
// (22) and rss (24, in pages); utime and stime are in clock ticks.
func parseStat(pid int, text []byte, pageSize uint64) (Process, bool) {
	open := bytes.IndexByte(text, '(')
	end := bytes.LastIndexByte(text, ')')
	if open < 0 || end < open {
		return Process{}, false
	}
	fields := bytes.Fields(text[end+1:])
	// fields[0] is field 3, the state.
	field := func(n int) (uint64, bool) {
		if n-3 >= len(fields) {
			return 0, false
		}
		v, err := strconv.ParseUint(string(fields[n-3]), 10, 64)
		return v, err == nil
	}
	utime, ok1 := field(14)
	stime, ok2 := field(15)
	start, ok3 := field(22)
	rss, ok4 := field(24)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return Process{}, false
	}
	return Process{
		PID:    pid,
		Start:  start,
		Name:   string(text[open+1 : end]),
		CPU:    utime + stime,
		Memory: rss * pageSize,
	}, true
}
