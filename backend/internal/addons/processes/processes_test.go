package processes

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func TestParseStatReadsNamesWithSpacesAndParentheses(t *testing.T) {
	text := []byte("4242 (Web Content (x)) S 1 4242 4242 0 -1 4194560 100 0 0 0 750 250 0 0 20 0 30 0 123456 1000000 2560 18446744073709551615 0 0 0 0 0 0 0 4096 0 0 0 0 17 3 0 0 0 0 0\n")

	got, ok := parseStat(4242, text, 4096)

	want := Process{PID: 4242, Start: 123456, Name: "Web Content (x)", CPU: 1000, Memory: 2560 * 4096}
	if !ok || got != want {
		t.Errorf("parseStat() = %+v, %v; want %+v", got, ok, want)
	}
	if _, ok := parseStat(1, []byte("1 (init) S 1 2 3\n"), 4096); ok {
		t.Error("parseStat() of a short line is ok, want false")
	}
}

func TestParseTotalAddsUpEveryCPU(t *testing.T) {
	total, ok := parseTotal([]byte("cpu  100 2 30 1000 5 1 2 3 7 0\ncpu0 50 1 15 500 2 0 1 1 3 0\n"))
	if !ok || total != 1143 {
		t.Errorf("parseTotal() = %v, %v; want 1143", total, ok)
	}
	if _, ok := parseTotal([]byte("intr 1 2 3\n")); ok {
		t.Error("parseTotal() without a cpu line is ok, want false")
	}
}

func TestParseUptimeCountsClockTicks(t *testing.T) {
	ticks, ok := parseUptime([]byte("350735.47 234388.90\n"))
	if !ok || ticks != 35073547 {
		t.Errorf("parseUptime() = %v, %v; want 35073547", ticks, ok)
	}
	for _, text := range []string{"", "soon 1.00", "-1.00 1.00", "NaN 1.00"} {
		if _, ok := parseUptime([]byte(text)); ok {
			t.Errorf("parseUptime(%q) is ok, want false", text)
		}
	}
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// stat is a /proc/<pid>/stat line with the given CPU ticks and resident pages.
func stat(pid, name, ticks, pages string) string {
	return pid + " (" + name + ") S 1 1 1 0 -1 0 0 0 0 0 " + ticks + " 0 0 0 20 0 1 0 77 0 " + pages + " 0\n"
}

func TestProcFSReadsEveryProcess(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"uptime":       "12.34 40.00\n",
		"stat":         "cpu  10 0 10 80 0 0 0 0 0 0\n",
		"1/stat":       stat("1", "systemd", "5", "3"),
		"20/stat":      stat("20", "sshd", "1", "2"),
		"self/stat":    stat("20", "sshd", "1", "2"),
		"20/status":    "not read",
		"bogus/stat":   "not a pid",
		"300/stat":     "broken",
		"meminfo":      "MemTotal: 1 kB\n",
		"sys/kernel/x": "",
	})

	got, err := newProcFS(dir, 4096).sample()

	if err != nil || got.Total != 100 || got.Time != 1234 || len(got.Processes) != 2 {
		t.Fatalf("sample() = %+v, %v; want 2 processes, a total of 100 at 1234", got, err)
	}
	if got.Processes[0].Name != "systemd" || got.Processes[1].Memory != 2*4096 {
		t.Errorf("sample() = %+v", got.Processes)
	}
}

func TestSampleSaysWhyItFails(t *testing.T) {
	dir := t.TempDir()
	if _, err := newProcFS(dir, 4096).sample(); err == nil {
		t.Error("sample() of an empty folder did not fail")
	}
	if err := os.WriteFile(filepath.Join(dir, "uptime"), []byte("soon"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newProcFS(dir, 4096).sample(); err == nil {
		t.Error("sample() without a time since boot did not fail")
	}
}

func values(group metrics.Extra) map[string]float64 {
	v := map[string]float64{}
	for _, item := range group.Items {
		v[item.ID+" "+item.Label] = *item.Value
	}
	return v
}

// source returns samples one by one.
func source(samples ...Sample) Source {
	next := 0
	return func(time.Time) (Sample, error) { next++; return samples[next-1], nil }
}

func TestReaderRanksByCPUSinceThePreviousRead(t *testing.T) {
	samples := []Sample{
		{Total: 1000, Time: 8, Processes: []Process{
			{PID: 1, Start: 5, Name: "init", CPU: 100, Memory: 10},
			{PID: 2, Start: 5, Name: "worker", CPU: 500, Memory: 300},
			{PID: 3, Start: 5, Name: "old", CPU: 900, Memory: 20},
		}},
		{Total: 1400, Time: 12, Processes: []Process{
			{PID: 1, Start: 5, Name: "init", CPU: 100, Memory: 10},
			{PID: 2, Start: 5, Name: "worker", CPU: 700, Memory: 300},
			// PID 3 ended and a new process got its PID.
			{PID: 3, Start: 9, Name: "worker", CPU: 40, Memory: 200},
		}},
	}
	r := NewReader(source(samples...))

	first := r.Read(time.Now())
	if len(first) != 1 || first[0].ID != "processes-memory" {
		t.Fatalf("first Read() = %+v, want memory only", first)
	}
	second := r.Read(time.Now())
	if len(second) != 2 || second[0].ID != "processes-cpu" || second[1].ID != "processes-memory" {
		t.Fatalf("second Read() = %+v, want CPU and memory", second)
	}

	cpu := values(second[0])
	if len(cpu) != 2 || cpu["pid-2 worker"] != 50 || cpu["pid-3 worker (2)"] != 10 {
		t.Errorf("CPU = %v, want worker 50 %% and the new worker (2) 10 %%", cpu)
	}
	if second[0].Items[0].Unit != metrics.UnitPercent || second[0].Items[0].History {
		t.Errorf("CPU item = %+v, want a percent without history", second[0].Items[0])
	}
	memory := second[1].Items
	if len(memory) != 3 || memory[0].ID != "pid-2" || memory[1].ID != "pid-3" || memory[2].ID != "pid-1" ||
		*memory[0].Value != 300 || memory[0].Unit != metrics.UnitBytes {
		t.Errorf("memory = %+v, want worker, worker, init", memory)
	}
}

func TestReaderWaitsForAProcessItMissed(t *testing.T) {
	r := NewReader(source(
		// The server's stat could not be read this time.
		Sample{Total: 1000, Time: 100, Processes: []Process{{PID: 1, Start: 5, Name: "init", CPU: 10}}},
		Sample{Total: 1400, Time: 200, Processes: []Process{
			{PID: 1, Start: 5, Name: "init", CPU: 10},
			{PID: 2, Start: 50, Name: "server", CPU: 90000},
			{PID: 3, Start: 150, Name: "job", CPU: 40},
		}},
		Sample{Total: 1800, Time: 300, Processes: []Process{
			{PID: 2, Start: 50, Name: "server", CPU: 90100},
			{PID: 3, Start: 150, Name: "job", CPU: 80},
		}},
	))
	r.Read(time.Now())

	// The job started after the previous sample, so all its CPU time counts;
	// the server is older and only went unread, so it waits a sample.
	if cpu := values(r.Read(time.Now())[0]); len(cpu) != 1 || cpu["pid-3 job"] != 10 {
		t.Errorf("CPU = %v, want only the new job at 10 %%", cpu)
	}
	if cpu := values(r.Read(time.Now())[0]); len(cpu) != 2 || cpu["pid-2 server"] != 25 || cpu["pid-3 job"] != 10 {
		t.Errorf("CPU = %v, want the server at 25 %% once it was read twice", cpu)
	}
}

func TestCPUSumAddsUpIdleAndEveryProcessAsTaskManagerDoes(t *testing.T) {
	var sum cpuSum
	if got := sum.add(100, 5000, []Process{{PID: 4, Start: 1, CPU: 300}, {PID: 8, Start: 2, CPU: 700}}); got != 0 {
		t.Errorf("first add() = %d, want 0", got)
	}
	// 600 idle, 100 by PID 4, 50 by PID 9, which started since, and nothing
	// counted for PID 8, which ended.
	got := sum.add(200, 5600, []Process{{PID: 4, Start: 1, CPU: 400}, {PID: 9, Start: 150, CPU: 50}})
	if got != 750 {
		t.Errorf("add() = %d, want 750", got)
	}
	if got := sum.add(300, 5700, []Process{{PID: 4, Start: 1, CPU: 400}}); got != 850 {
		t.Errorf("add() = %d, want 850, counted on", got)
	}
}

func TestReaderListsTheTopTen(t *testing.T) {
	var processes []Process
	for pid := 1; pid <= 25; pid++ {
		processes = append(processes, Process{PID: pid, Name: "p", Memory: uint64(pid)})
	}
	r := NewReader(func(time.Time) (Sample, error) { return Sample{Processes: processes, Total: 1}, nil })

	got := r.Read(time.Now())

	if len(got) != 1 || len(got[0].Items) != Top || got[0].Items[0].ID != "pid-25" || got[0].Items[9].Label != "p (10)" {
		t.Errorf("Read() = %+v, want the ten largest, numbered", got)
	}
	if clean := metrics.CleanExtras(got, 64); len(clean) != 1 || len(clean[0].Items) != Top {
		t.Errorf("CleanExtras() dropped values: %+v", clean)
	}
}

func TestReaderWithoutSourceReturnsNothing(t *testing.T) {
	r := NewReader(func(time.Time) (Sample, error) { return Sample{}, errors.New("no /proc") })
	if got := r.Read(time.Now()); got != nil {
		t.Errorf("Read() = %+v, want nothing", got)
	}
}

func TestReaderNotesWhenReadingFailsAndWorksAgain(t *testing.T) {
	var err error
	r := NewReader(func(time.Time) (Sample, error) { return Sample{Total: 1}, err })

	r.Read(time.Now())
	if r.failing {
		t.Error("failing = true after a read that worked")
	}
	err = errors.New("no /proc")
	r.Read(time.Now())
	if !r.failing {
		t.Error("failing = false after a read that failed, want it noted")
	}
	err = nil
	r.Read(time.Now())
	if r.failing {
		t.Error("failing = true once reading works again")
	}
}
