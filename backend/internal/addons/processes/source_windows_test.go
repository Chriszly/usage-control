package processes

import "testing"

// TestWindowsListsProcesses checks the one call that lists the processes on
// a real Windows machine: it has to find processes with names and memory,
// and the CPUs' idle time.
func TestWindowsListsProcesses(t *testing.T) {
	processes, idle, _ := ntProcesses(nil)
	if len(processes) == 0 {
		t.Fatal("ntProcesses() found no processes")
	}
	if idle == 0 {
		t.Error("idle = 0, want the CPUs' idle time")
	}
	named := 0
	for _, p := range processes {
		if p.Name != "" && p.Memory > 0 {
			named++
		}
	}
	if named == 0 {
		t.Errorf("no process has a name and memory: %+v", processes[:min(5, len(processes))])
	}
	t.Logf("%d processes, %d with a name and memory", len(processes), named)
}
