// Package containers reads the CPU and memory each running container uses,
// for the containers add-on: from the files of the kernel's cgroups (v2
// only), without asking Docker or Podman, since access to their socket is as
// good as root. Docker's containers are named from their settings in
// /var/lib/docker/containers when those can be read, else by their short id.
//
// It only reads; nothing in here changes the machine.
package containers

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// MaxContainers is the most containers the add-on reports, as many values as
// a hub keeps of a group by default.
const MaxContainers = 64

// maxDepth is how deep below the cgroup root containers are looked for:
// rootless Podman's are five folders down, in the user's own slice.
const maxDepth = 6

// Container is what one running container uses.
type Container struct {
	// ID is the container's full id, 64 hexadecimal digits.
	ID string
	// Name is the container's name, or its short id when it is not known.
	Name string
	// CPU is the percent of the whole machine's CPU the container used since
	// the previous read; nil at the first read of a container.
	CPU *float64
	// MemoryBytes is the memory the container uses, without the file cache
	// the kernel can drop, as docker stats counts it.
	MemoryBytes uint64
}

// Reader reads the containers of the machine.
type Reader struct {
	cgroups string
	docker  string
	cpus    int
	// previous is each container's CPU time at the previous read, in
	// microseconds, by id.
	previous map[string]uint64
	at       time.Time
	// names are the names already read, by id.
	names map[string]string
}

// NewReader returns a Reader for the machine. sysDir is where /sys is, which
// in a container is where the host's /sys is mounted, and dockerDir is
// Docker's folder of container settings.
func NewReader(sysDir, dockerDir string) *Reader {
	cgroups := filepath.Join(sysDir, "fs", "cgroup")
	return &Reader{
		cgroups: cgroups,
		docker:  dockerDir,
		cpus:    countCPUs(readText(filepath.Join(cgroups, "cpuset.cpus.effective"))),
		names:   map[string]string{},
	}
}

// Read returns the running containers, sorted by name. CPU is the average
// since the previous call, so the first call leaves it out.
func (r *Reader) Read(now time.Time) []Container {
	// cgroup.controllers is only in the root of cgroup v2.
	if _, err := os.Stat(filepath.Join(r.cgroups, "cgroup.controllers")); err != nil {
		return nil
	}
	found := map[string]string{}
	findContainers(r.cgroups, 0, found)

	current := map[string]uint64{}
	names := map[string]string{}
	seconds := now.Sub(r.at).Seconds()
	containers := make([]Container, 0, len(found))
	for id, dir := range found {
		memory, ok := readUint(filepath.Join(dir, "memory.current"))
		if !ok {
			continue // The container stopped while it was read.
		}
		c := Container{ID: id, Name: r.name(id), MemoryBytes: memory}
		names[id] = c.Name
		if inactive, ok := statValue(filepath.Join(dir, "memory.stat"), "inactive_file"); ok && inactive < memory {
			c.MemoryBytes = memory - inactive
		}
		if usage, ok := statValue(filepath.Join(dir, "cpu.stat"), "usage_usec"); ok {
			current[id] = usage
			if before, known := r.previous[id]; known && seconds > 0 && usage >= before && r.cpus > 0 {
				percent := float64(usage-before) / 1e6 / seconds / float64(r.cpus) * 100
				c.CPU = &percent
			}
		}
		containers = append(containers, c)
	}
	r.previous, r.at, r.names = current, now, names
	slices.SortFunc(containers, func(a, b Container) int {
		return strings.Compare(a.Name+"\x00"+a.ID, b.Name+"\x00"+b.ID)
	})
	if len(containers) > MaxContainers {
		containers = containers[:MaxContainers]
	}
	return containers
}

// name returns the container's name, read once per container.
func (r *Reader) name(id string) string {
	if name, ok := r.names[id]; ok {
		return name
	}
	if name := dockerName(r.docker, id); name != "" {
		return name
	}
	return id[:12]
}

var (
	// scope matches the cgroup of a container with the systemd cgroup driver:
	// docker-<id>.scope, libpod-<id>.scope (Podman), cri-containerd-<id>.scope
	// and crio-<id>.scope (Kubernetes).
	scope = regexp.MustCompile(`^(?:docker|libpod|cri-containerd|crio)-([0-9a-f]{64})\.scope$`)
	// bareID matches it with the cgroupfs driver, such as /docker/<id>.
	bareID = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// findContainers adds the cgroup of each container below dir to found, by
// container id. It does not look inside a container's cgroup, which may hold
// cgroups of its own.
func findContainers(dir string, depth int, found map[string]string) {
	if depth >= maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(dir, name)
		if m := scope.FindStringSubmatch(name); m != nil {
			found[m[1]] = path
			continue
		}
		if bareID.MatchString(name) {
			found[name] = path
			continue
		}
		findContainers(path, depth+1, found)
	}
}

// dockerName reads a Docker container's name from its settings,
// <dir>/<id>/config.v2.json, which only root may read. It is "" when they
// cannot be read, such as for a Podman container.
func dockerName(dir, id string) string {
	data, err := os.ReadFile(filepath.Join(dir, id, "config.v2.json")) //nolint:gosec // id is 64 hexadecimal digits, in Docker's folder
	if err != nil {
		return ""
	}
	var config struct{ Name string }
	if json.Unmarshal(data, &config) != nil {
		return ""
	}
	return strings.TrimPrefix(config.Name, "/")
}

// Extras returns the containers as two groups of extras: their CPU and
// their memory, each value under the container's short id.
func Extras(containers []Container) []metrics.Extra {
	if len(containers) == 0 {
		return nil
	}
	cpu := metrics.Extra{
		ID:     "containers-cpu",
		Title:  "Containers: CPU",
		Titles: map[string]string{"de": "Container: CPU", "fr": "Conteneurs : processeur", "es": "Contenedores: CPU"}, //nolint:misspell // French
	}
	memory := metrics.Extra{
		ID:     "containers-memory",
		Title:  "Containers: memory",
		Titles: map[string]string{"de": "Container: Arbeitsspeicher", "fr": "Conteneurs : mémoire", "es": "Contenedores: memoria"}, //nolint:misspell // French
	}
	for _, c := range containers {
		id := c.ID[:12]
		if c.CPU != nil {
			cpu.Items = append(cpu.Items, metrics.ExtraItem{
				ID: id, Label: c.Name, Unit: metrics.UnitPercent, Value: c.CPU, History: true,
			})
		}
		bytes := float64(c.MemoryBytes)
		memory.Items = append(memory.Items, metrics.ExtraItem{
			ID: id, Label: c.Name, Unit: metrics.UnitBytes, Value: &bytes, History: true,
		})
	}
	if len(cpu.Items) == 0 {
		return []metrics.Extra{memory}
	}
	return []metrics.Extra{cpu, memory}
}

// countCPUs counts the CPUs of a list such as "0-3,6"; for an empty or
// unreadable list it is the CPUs this program may use.
func countCPUs(list string) int {
	count := 0
	for part := range strings.SplitSeq(list, ",") {
		first, last, isRange := strings.Cut(strings.TrimSpace(part), "-")
		from, err := strconv.Atoi(first)
		if err != nil {
			return runtime.NumCPU()
		}
		to := from
		if isRange {
			if to, err = strconv.Atoi(last); err != nil || to < from {
				return runtime.NumCPU()
			}
		}
		count += to - from + 1
	}
	return count
}

// statValue reads the value of key from a file of "key value" lines, such as
// cpu.stat or memory.stat.
func statValue(path, key string) (uint64, bool) {
	file, err := os.Open(path) //nolint:gosec // a cgroup file, below the folder its setting names
	if err != nil {
		return 0, false
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, value, ok := strings.Cut(scanner.Text(), " ")
		if ok && name == key {
			n, err := strconv.ParseUint(value, 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// readText reads a short file, or returns "" when it cannot be read.
func readText(path string) string {
	text, err := os.ReadFile(path) //nolint:gosec // a cgroup file, below the folder its setting names
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(text))
}

// readUint reads a file that holds one whole number.
func readUint(path string) (uint64, bool) {
	n, err := strconv.ParseUint(readText(path), 10, 64)
	return n, err == nil
}

// HostSys returns where /sys is: HOST_SYS in a container that mounts the
// host's /sys there, else /sys.
func HostSys() string {
	if dir := os.Getenv("HOST_SYS"); dir != "" {
		return dir
	}
	return "/sys"
}

// DockerDir is Docker's folder of container settings.
const DockerDir = "/var/lib/docker/containers"
