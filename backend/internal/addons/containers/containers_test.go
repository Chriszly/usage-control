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
	// The rate is worked out over the monotonic clock, which String shows as m=.
	if !strings.Contains(r.at.String(), " m=") {
		t.Errorf("Read() kept the time %v, want it with its monotonic reading", r.at)
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
	if web.Name != "web" || web.MemoryBytes == nil || *web.MemoryBytes != 2000000 || web.CPU == nil || *web.CPU != 50 {
		t.Errorf("web = %+v, want 2 MB and 8 s of CPU in 4 s on 4 CPUs = 50%%", web)
	}
	if got[0].CPU == nil || *got[0].CPU != 12.5 {
		t.Errorf("db CPU = %v, want 12.5%%", got[0].CPU)
	}
	if got[1].CPU != nil {
		t.Errorf("podman CPU = %v, want none without cpu.stat", *got[1].CPU)
	}
}

func TestReadReportsCPUWithoutTheMemoryController(t *testing.T) {
	sys := t.TempDir()
	// As on Raspberry Pi OS without cgroup_enable=memory: no memory files.
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/cgroup.controllers":                               "cpuset cpu io pids",
		"fs/cgroup/cpuset.cpus.effective":                            "0-3",
		"fs/cgroup/system.slice/docker-" + webID + ".scope/cpu.stat": "usage_usec 1000000\n",
		// A cgroup without cpu.stat or memory.current is a container that stopped.
		"fs/cgroup/system.slice/docker-" + dbID + ".scope/cgroup.type": "domain",
	})
	r := NewReader(sys, t.TempDir())
	start := time.Now()
	r.Read(start)
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/system.slice/docker-" + webID + ".scope/cpu.stat": "usage_usec 3000000\n",
	})

	got := r.Read(start.Add(4 * time.Second))

	if len(got) != 1 || got[0].ID != webID || got[0].MemoryBytes != nil || got[0].CPU == nil || *got[0].CPU != 12.5 {
		t.Fatalf("Read() = %+v, want web at 12.5%% CPU without memory", got)
	}
	if extras := Extras(got); len(extras) != 1 || extras[0].ID != "containers-cpu" {
		t.Errorf("Extras() = %+v, want only the CPU", extras)
	}
}

func TestReadReadsNamesAgainWhenTheSettingsChange(t *testing.T) {
	sys, docker := t.TempDir(), t.TempDir()
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/cgroup.controllers":                                     "cpu memory",
		"fs/cgroup/system.slice/docker-" + webID + ".scope/memory.current": "1000",
	})
	r := NewReader(sys, docker)
	name := func() string {
		t.Helper()
		got := r.Read(time.Now())
		if len(got) != 1 {
			t.Fatalf("Read() = %+v, want one container", got)
		}
		return got[0].Name
	}
	settings := filepath.Join(docker, webID, "config.v2.json")
	write := func(text string, modified time.Time) {
		t.Helper()
		writeFiles(t, docker, map[string]string{webID + "/config.v2.json": text})
		if err := os.Chtimes(settings, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Hour)

	// Docker has not written the settings yet: the short id, until it has.
	if got := name(); got != webID[:12] {
		t.Errorf("name without settings = %q, want the short id", got)
	}
	write(`{"Name":"/web"}`, at)
	if got := name(); got != "web" {
		t.Errorf("name = %q, want web once the settings can be read", got)
	}
	// docker rename writes the settings anew, here at the same size.
	write(`{"Name":"/api"}`, at.Add(time.Second))
	if got := name(); got != "api" {
		t.Errorf("name after a rename = %q, want api", got)
	}
	// Settings that cannot be read keep the name read before.
	write(`{"Na`, at.Add(2*time.Second))
	if got := name(); got != "api" {
		t.Errorf("name with broken settings = %q, want api still", got)
	}
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}
	if got := name(); got != "api" {
		t.Errorf("name without settings = %q, want api still", got)
	}
}

func TestDockerDirFollowsTheSetting(t *testing.T) {
	t.Setenv("DOCKER_DIR", "")
	if got := DockerDir(); got != filepath.FromSlash("/var/lib/docker/containers") {
		t.Errorf("DockerDir() = %q, want /var/lib/docker/containers", got)
	}
	t.Setenv("DOCKER_DIR", "/srv/docker")
	if got := DockerDir(); got != filepath.FromSlash("/srv/docker/containers") {
		t.Errorf("DockerDir() = %q, want /srv/docker/containers", got)
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

func TestReadNotesOnceWhatItCannotRead(t *testing.T) {
	r := NewReader(t.TempDir(), filepath.Join(t.TempDir(), "missing"))
	r.Read(time.Now())
	if !r.noCgroupV2 {
		t.Error("noCgroupV2 = false without cgroup v2, want it noted")
	}

	sys := t.TempDir()
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/cgroup.controllers":                                  "cpu memory",
		"fs/cgroup/system.slice/libpod-" + podmanID + ".scope/cpu.stat": "usage_usec 1",
	})
	r = NewReader(sys, filepath.Join(t.TempDir(), "missing"))
	r.Read(time.Now())
	if r.noDockerDir {
		t.Error("noDockerDir = true with only a Podman container, want nothing noted")
	}
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/system.slice/docker-" + webID + ".scope/cpu.stat": "usage_usec 1",
	})
	r.Read(time.Now())
	if !r.noDockerDir {
		t.Error("noDockerDir = false with a Docker container and no Docker folder, want it noted")
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
	webMemory, dbMemory := uint64(2048), uint64(1024)
	got := Extras([]Container{
		{ID: webID, Name: "web", CPU: &percent, MemoryBytes: &webMemory},
		{ID: dbID, Name: dbID[:12], MemoryBytes: &dbMemory},
	})
	if len(got) != 2 || got[0].ID != "containers-cpu" || got[1].ID != "containers-memory" {
		t.Fatalf("Extras() = %+v, want a CPU and a memory group", got)
	}
	if items := got[0].Items; len(items) != 1 || items[0].ID != "web" || items[0].Unit != metrics.UnitPercent || *items[0].Value != 12.5 || !items[0].History {
		t.Errorf("CPU items = %+v, want web at 12.5%% with history", items)
	}
	if items := got[1].Items; len(items) != 2 || items[1].ID != dbID[:12] || items[1].Label != dbID[:12] || *items[1].Value != 1024 || items[1].Unit != metrics.UnitBytes {
		t.Errorf("memory items = %+v, want both containers in bytes", items)
	}
	if clean := metrics.CleanExtras(got, 64); len(clean) != 2 || len(clean[1].Items) != 2 {
		t.Errorf("CleanExtras() kept %+v, want everything", clean)
	}
	if only := Extras([]Container{{ID: dbID, Name: "db", MemoryBytes: &dbMemory}}); len(only) != 1 || only[0].ID != "containers-memory" {
		t.Errorf("Extras() without CPU = %+v, want only memory", only)
	}
	if none := Extras([]Container{{ID: dbID, Name: "db"}}); none != nil {
		t.Errorf("Extras() without values = %+v, want nothing", none)
	}
}

func TestItemIDFollowsTheName(t *testing.T) {
	// A container made anew by docker compose up keeps its name, not its id.
	if a, b := itemID(Container{ID: webID, Name: "app-web-1"}), itemID(Container{ID: dbID, Name: "app-web-1"}); a != "app-web-1" || b != a {
		t.Errorf("itemID() = %q and %q, want app-web-1 for both", a, b)
	}
	if got := itemID(Container{ID: webID, Name: webID[:12]}); got != webID[:12] {
		t.Errorf("itemID() without a name = %q, want the short id", got)
	}
	percent := 1.0
	long := strings.Repeat("monitoring_", 5)
	names := []string{"My.App", "my-app", long + "a", long + "b", "_"}
	seen := map[string]bool{}
	for _, name := range names {
		id := itemID(Container{ID: webID, Name: name})
		item := metrics.Extra{ID: "g", Items: []metrics.ExtraItem{{ID: id, Unit: metrics.UnitPercent, Value: &percent}}}
		if len(metrics.CleanExtras([]metrics.Extra{item}, 1)) != 1 || seen[id] {
			t.Errorf("itemID(%q) = %q, want a valid id of its own", name, id)
		}
		seen[id] = true
	}
	if got := itemID(Container{ID: webID, Name: "My.App"}); !strings.HasPrefix(got, "my-app-") || len(got) != len("my-app-")+8 {
		t.Errorf("itemID(My.App) = %q, want my-app- and a checksum", got)
	}
	got := Extras([]Container{{ID: webID, Name: "web", CPU: &percent}, {ID: dbID, Name: "web", CPU: &percent}})
	if items := got[0].Items; len(items) != 2 || items[0].ID != "web" || items[1].ID != dbID[:12] {
		t.Errorf("Extras() of two containers with one name = %+v, want the second by its short id", items)
	}
}

func TestReadNotesADockerContainerWithOnlyItsShortID(t *testing.T) {
	sys, docker := t.TempDir(), t.TempDir()
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/cgroup.controllers":                               "cpu memory",
		"fs/cgroup/system.slice/docker-" + webID + ".scope/cpu.stat": "usage_usec 1",
	})
	// Docker's folder is there, but holds other containers, as after moving
	// Docker's data elsewhere.
	writeFiles(t, docker, map[string]string{dbID + "/config.v2.json": `{"Name":"/old"}`})
	r := NewReader(sys, docker)

	if got := r.Read(time.Now()); len(got) != 1 || got[0].Name != webID[:12] {
		t.Fatalf("Read() = %+v, want web by its short id", got)
	}
	if r.noDockerDir || !r.shortDockerID {
		t.Errorf("noDockerDir = %v, shortDockerID = %v, want only the short id noted", r.noDockerDir, r.shortDockerID)
	}
}

func TestReadNamesPodmanContainersFromItsLists(t *testing.T) {
	sys, storage := t.TempDir(), t.TempDir()
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/cgroup.controllers":                                   "cpu memory",
		"fs/cgroup/machine.slice/libpod-" + podmanID + ".scope/cpu.stat": "usage_usec 1",
		"fs/cgroup/system.slice/crio-" + dbID + ".scope/cpu.stat":        "usage_usec 1",
		"fs/cgroup/machine.slice/libpod-" + webID + ".scope/cpu.stat":    "usage_usec 1",
	})
	writeFiles(t, storage, map[string]string{
		"overlay-containers/containers.json": `[{"id":"` + podmanID + `","names":["nextcloud"],"image":"x"},{"id":"` + dbID + `","names":[]}]`,
		// The list of another storage driver.
		"vfs-containers/containers.json": `[{"id":"` + webID + `","names":["web"]}]`,
	})
	list := filepath.Join(storage, "overlay-containers", "containers.json")
	r := NewReader(sys, t.TempDir())
	r.podman = storage

	got := r.Read(time.Now())
	if len(got) != 3 || got[0].Name != dbID[:12] || got[1].Name != "nextcloud" || got[2].Name != "web" {
		t.Fatalf("Read() = %+v, want bbb… by its short id, nextcloud and web", got)
	}

	// The list is read again when it changes, as on podman rename. One that
	// cannot be understood, as while Podman writes it, keeps the names
	// before and is read again at the next read.
	changed := func(text string, modified time.Time) {
		t.Helper()
		writeFiles(t, storage, map[string]string{"overlay-containers/containers.json": text})
		if err := os.Chtimes(list, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	later := time.Now().Add(time.Minute)
	changed(`[{"id":"`+podmanID+`","names":["clo`, later)
	if got := r.Read(time.Now()); len(got) != 3 || got[1].Name != "nextcloud" {
		t.Errorf("Read() = %+v, want the name before while the list cannot be read", got)
	}
	changed(`[{"id":"`+podmanID+`","names":["cloud"]}]`, later)
	if got := r.Read(time.Now()); len(got) != 3 || got[1].Name != "cloud" {
		t.Errorf("Read() = %+v, want the new name cloud", got)
	}
}

func TestReadWalksTheCgroupTreeWhenCgroupsComeOrGo(t *testing.T) {
	sys := t.TempDir()
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/cgroup.controllers":                               "cpu memory",
		"fs/cgroup/cgroup.stat":                                      "nr_descendants 40\nnr_dying_descendants 0\n",
		"fs/cgroup/system.slice/docker-" + webID + ".scope/cpu.stat": "usage_usec 1",
	})
	r := NewReader(sys, t.TempDir())
	r.Read(time.Now())

	// A container next to a known one shows at once.
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/system.slice/docker-" + dbID + ".scope/cpu.stat": "usage_usec 1",
	})
	if got := r.Read(time.Now()); len(got) != 2 {
		t.Errorf("Read() = %+v, want the new container next to web", got)
	}

	// One in a new place shows once the number of cgroups changes.
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/machine.slice/libpod-" + podmanID + ".scope/cpu.stat": "usage_usec 1",
	})
	if got := r.Read(time.Now()); len(got) != 2 {
		t.Errorf("Read() = %+v, want no walk while the number of cgroups stays", got)
	}
	writeFiles(t, sys, map[string]string{"fs/cgroup/cgroup.stat": "nr_descendants 42\n"})
	if got := r.Read(time.Now()); len(got) != 3 {
		t.Errorf("Read() = %+v, want all three containers once there are more cgroups", got)
	}

	// Should the number stay the same, the walk every walkEvery reads finds
	// a new one.
	nextID := strings.Repeat("d", 64)
	writeFiles(t, sys, map[string]string{
		"fs/cgroup/kubepods.slice/cri-containerd-" + nextID + ".scope/cpu.stat": "usage_usec 1",
	})
	for range walkEvery - 1 {
		r.Read(time.Now())
	}
	if got := r.Read(time.Now()); len(got) != 4 {
		t.Errorf("Read() at the walk every walkEvery reads = %+v, want four containers", got)
	}

	// A container that stops is gone at once.
	if err := os.RemoveAll(filepath.Join(sys, "fs/cgroup/system.slice/docker-"+webID+".scope")); err != nil {
		t.Fatal(err)
	}
	if got := r.Read(time.Now()); len(got) != 3 {
		t.Errorf("Read() = %+v, want three containers once web stopped", got)
	}
}
