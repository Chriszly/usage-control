package containers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

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

var (
	webID    = strings.Repeat("a", 64)
	dbID     = strings.Repeat("b", 64)
	podmanID = strings.Repeat("c", 64)
)

func TestReadFindsContainersAndMeasuresCPUSinceThePreviousRead(t *testing.T) {
	sys, docker := t.TempDir(), t.TempDir()
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/cgroup.controllers":                      "cpuset cpu io memory pids",
		"fs/cgroup/cpuset.cpus.effective":                   "0-3",
		"fs/cgroup/system.slice/ssh.service/memory.current": "1000",
		// Docker with the systemd driver, with a cgroup of its own inside.
		"fs/cgroup/system.slice/docker-" + webID + ".scope/memory.current":      "3000000",
		"fs/cgroup/system.slice/docker-" + webID + ".scope/memory.stat":         "anon 2000000\ninactive_file 1000000\n",
		"fs/cgroup/system.slice/docker-" + webID + ".scope/cpu.stat":            "usage_usec 1000000\nuser_usec 800000\n",
		"fs/cgroup/system.slice/docker-" + webID + ".scope/init.scope/cpu.stat": "usage_usec 5\n",
		// Docker with the cgroupfs driver.
		"fs/cgroup/docker/" + dbID + "/memory.current": "500",
		"fs/cgroup/docker/" + dbID + "/cpu.stat":       "usage_usec 0\n",
		// Rootless Podman; its conmon is not a container.
		"fs/cgroup/user.slice/user-1000.slice/user@1000.service/user.slice/libpod-" + podmanID + ".scope/memory.current": "700",
		"fs/cgroup/machine.slice/libpod-conmon-" + podmanID + ".scope/memory.current":                                    "9",
	})
	writeFiles(t, docker, map[string]string{
		webID + "/config.v2.json": `{"ID":"` + webID + `","Name":"/web","State":{"Running":true}}`,
		dbID + "/config.v2.json":  `not json`,
	})
	r := NewReader(sys, docker)
	start := time.Now()

	first := r.Read(start)
	if len(first) != 3 {
		t.Fatalf("first Read() = %+v, want three containers", first)
	}
	for _, c := range first {
		if c.CPU != nil {
			t.Errorf("first Read() of %s has CPU %v, want none yet", c.Name, *c.CPU)
		}
	}
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/system.slice/docker-" + webID + ".scope/cpu.stat": "usage_usec 9000000\n",
		"fs/cgroup/docker/" + dbID + "/cpu.stat":                     "usage_usec 2000000\n",
	})
	got := r.Read(start.Add(4 * time.Second))

	if len(got) != 3 || got[0].ID != dbID || got[1].ID != podmanID || got[2].ID != webID {
		t.Fatalf("Read() = %+v, want bbb…, ccc…, web by name", got)
	}
	if got[0].Name != dbID[:12] || got[1].Name != podmanID[:12] {
		t.Errorf("names = %q, %q, want the short ids", got[0].Name, got[1].Name)
	}
	web := got[2]
	if web.Name != "web" || web.MemoryBytes != 2000000 || web.CPU == nil || *web.CPU != 50 {
		t.Errorf("web = %+v, want 2 MB and 8 s of CPU in 4 s on 4 CPUs = 50%%", web)
	}
	if got[0].CPU == nil || *got[0].CPU != 12.5 {
		t.Errorf("db CPU = %v, want 12.5%%", got[0].CPU)
	}
	if got[1].CPU != nil {
		t.Errorf("podman CPU = %v, want none without cpu.stat", *got[1].CPU)
	}
}

func TestReadIgnoresCgroupV1(t *testing.T) {
	sys := t.TempDir()
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/memory/docker/" + webID + "/memory.usage_in_bytes": "1",
	})
	if got := NewReader(sys, t.TempDir()).Read(time.Now()); got != nil {
		t.Errorf("Read() = %+v, want nothing on cgroup v1", got)
	}
}

func TestCountCPUs(t *testing.T) {
	for list, want := range map[string]int{"0-3": 4, "0-3,6,8-9": 7, "0": 1} {
		if got := countCPUs(list); got != want {
			t.Errorf("countCPUs(%q) = %d, want %d", list, got, want)
		}
	}
	if got := countCPUs(""); got < 1 {
		t.Errorf("countCPUs(\"\") = %d, want this machine's CPUs", got)
	}
}

func TestExtras(t *testing.T) {
	if got := Extras(nil); got != nil {
		t.Errorf("Extras(nil) = %+v, want nothing", got)
	}
	percent := 12.5
	got := Extras([]Container{
		{ID: webID, Name: "web", CPU: &percent, MemoryBytes: 2048},
		{ID: dbID, Name: dbID[:12], MemoryBytes: 1024},
	})
	if len(got) != 2 || got[0].ID != "containers-cpu" || got[1].ID != "containers-memory" {
		t.Fatalf("Extras() = %+v, want a CPU and a memory group", got)
	}
	if items := got[0].Items; len(items) != 1 || items[0].ID != webID[:12] || items[0].Unit != metrics.UnitPercent || *items[0].Value != 12.5 || !items[0].History {
		t.Errorf("CPU items = %+v, want web at 12.5%% with history", items)
	}
	if items := got[1].Items; len(items) != 2 || items[1].Label != dbID[:12] || *items[1].Value != 1024 || items[1].Unit != metrics.UnitBytes {
		t.Errorf("memory items = %+v, want both containers in bytes", items)
	}
	if clean := metrics.CleanExtras(got, 64); len(clean) != 2 || len(clean[1].Items) != 2 {
		t.Errorf("CleanExtras() kept %+v, want everything", clean)
	}
	if only := Extras([]Container{{ID: dbID, Name: "db"}}); len(only) != 1 || only[0].ID != "containers-memory" {
		t.Errorf("Extras() without CPU = %+v, want only memory", only)
	}
}
