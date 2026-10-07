// Package containers reads the CPU and memory each running container uses,
// for the containers add-on: from the files of the kernel's cgroups (v2
// only), without asking Docker or Podman, since access to their socket is as
// good as root. Docker's containers are named from their settings in its
// data folder, /var/lib/docker/containers by default, and those of rootful
// Podman and CRI-O from the one list of containers they keep, when those can
// be read, else by their short id. containerd keeps its names in a database
// file, which is not read, so its containers show by their short id.
//
// It only reads; nothing in here changes the machine.
package containers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
	"github.com/Chriszly/usage-control/backend/internal/sysfile"
)

// MaxContainers is the most containers the add-on reports, as many values as
// a hub keeps of a group by default.
const MaxContainers = 64

// maxDepth is how deep below the cgroup root containers are looked for:
// rootless Podman's are five folders down, in the user's own slice.
const maxDepth = 6

// walkEvery is how often the whole cgroup tree is walked for containers, in
// reads. In between only the folders that held containers at the last walk
// are looked at, which finds containers that start or stop there, such as
// Docker's in system.slice, at once, and containers in new places, such as
// the first of a Kubernetes pod, at the next walk.
const walkEvery = 12

// PodmanContainers is the list of containers of rootful Podman and CRI-O,
// with their names.
const PodmanContainers = "/var/lib/containers/storage/overlay-containers/containers.json"

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
	// the kernel can drop, as docker stats counts it; nil where the kernel
	// counts no memory per container, as Raspberry Pi OS's does unless it is
	// booted with cgroup_enable=memory.
	MemoryBytes *uint64
}

// Reader reads the containers of the machine.
type Reader struct {
	cgroups string
	docker  string
	cpus    int
	// previous is each container's CPU time at the previous read, in
	// microseconds, by id.
	previous map[string]uint64
	// at is the time of the previous read as Read got it, with Go's monotonic
	// clock reading, so the CPU's rate does not jump when the clock is set.
	at time.Time
	// names are the names read from Docker's settings, by id.
	names map[string]knownName
	// podman is the list of Podman's containers (see PodmanContainers),
	// podmanNames the names read from it, by id, and podmanFile the time and
	// size of the file then, to read it again only when it changes.
	podman      string
	podmanNames map[string]string
	podmanFile  knownName
	// reads counts the reads, for walkEvery, and parents are the folders
	// that held containers at the last walk.
	reads   int
	parents []string
	// noCgroupV2, noDockerDir and shortDockerID are set once that was
	// logged, so it is logged once and not at every read.
	noCgroupV2, noDockerDir, shortDockerID bool
}

// knownName is a name read from a Docker container's settings, with the
// time and size of the file then, to read it again only when it changes, as
// on docker rename.
type knownName struct {
	name     string
	modified time.Time
	size     int64
}

// NewReader returns a Reader for the machine. sysDir is where /sys is, which
// in a container is where the host's /sys is mounted, and dockerDir is
// Docker's folder of container settings.
func NewReader(sysDir, dockerDir string) *Reader {
	cgroups := filepath.Join(sysDir, "fs", "cgroup")
	return &Reader{
		cgroups: cgroups,
		docker:  dockerDir,
		cpus:    countCPUs(sysfile.Text(filepath.Join(cgroups, "cpuset.cpus.effective"))),
		names:   map[string]knownName{},
		podman:  PodmanContainers,
	}
}

// Read returns the running containers, sorted by name. CPU is the average
// since the previous call, so the first call leaves it out.
func (r *Reader) Read(now time.Time) []Container {
	// cgroup.controllers is only in the root of cgroup v2.
	if _, err := os.Stat(filepath.Join(r.cgroups, "cgroup.controllers")); err != nil {
		if !r.noCgroupV2 {
			r.noCgroupV2 = true
			slog.Warn("the kernel's cgroups are not version 2, which the add-on reads, so it reports no containers", "folder", r.cgroups)
		}
		return nil
	}
	found := r.find()
	r.readPodman()
	if !r.noDockerDir && anyDocker(found) {
		if _, err := os.Stat(r.docker); err != nil {
			r.noDockerDir = true
			slog.Warn("Docker's data folder cannot be read, so its containers are named by their short id; set DOCKER_DIR to the folder docker info shows as Docker Root Dir", "error", err)
		}
	}

	current := map[string]uint64{}
	names := map[string]knownName{}
	seconds := now.Sub(r.at).Seconds()
	containers := make([]Container, 0, len(found))
	for id, dir := range found {
		var c Container
		usage, hasCPU := statValue(filepath.Join(dir, "cpu.stat"), "usage_usec")
		if hasCPU {
			current[id] = usage
			if before, known := r.previous[id]; known && seconds > 0 && usage >= before && r.cpus > 0 {
				percent := float64(usage-before) / 1e6 / seconds / float64(r.cpus) * 100
				c.CPU = &percent
			}
		}
		// Without the kernel's memory controller there is no memory.current,
		// but the CPU is still counted.
		memory, hasMemory := sysfile.Uint(filepath.Join(dir, "memory.current"))
		if hasMemory {
			if inactive, ok := statValue(filepath.Join(dir, "memory.stat"), "inactive_file"); ok && inactive < memory {
				memory -= inactive
			}
			c.MemoryBytes = &memory
		}
		if !hasCPU && !hasMemory {
			continue // The container stopped while it was read.
		}
		c.ID, c.Name = id, r.name(id, isDocker(dir), names)
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

// name returns the container's name and adds what it read to names: from
// Docker's settings, read again whenever the file changes, such as on docker
// rename, else from Podman's list. While neither tells, it is the name read
// before, else the short id, and the next call tries again. docker is
// whether the container is Docker's; one that has only its short id is
// logged once.
func (r *Reader) name(id string, docker bool, names map[string]knownName) string {
	known, ok := r.names[id]
	path := filepath.Join(r.docker, id, "config.v2.json")
	if info, err := os.Stat(path); err == nil && (!ok || !info.ModTime().Equal(known.modified) || info.Size() != known.size) {
		if name := dockerName(path); name != "" {
			known, ok = knownName{name: name, modified: info.ModTime(), size: info.Size()}, true
		}
	}
	if ok {
		names[id] = known
		return known.name
	}
	if name := r.podmanNames[id]; name != "" {
		return name
	}
	if docker && !r.noDockerDir && !r.shortDockerID {
		r.shortDockerID = true
		slog.Warn("a Docker container's settings cannot be read, so it is named by its short id; if Docker keeps its data elsewhere, set DOCKER_DIR to the folder docker info shows as Docker Root Dir",
			"container", id[:12], "settings", path)
	}
	return id[:12]
}

// readPodman reads the names in Podman's list of containers when the file
// changed since it was read last. While it cannot be read, its names are
// forgotten.
func (r *Reader) readPodman() {
	info, err := os.Stat(r.podman)
	if err != nil {
		r.podmanNames, r.podmanFile = nil, knownName{}
		return
	}
	if info.ModTime().Equal(r.podmanFile.modified) && info.Size() == r.podmanFile.size {
		return
	}
	r.podmanNames = podmanNames(r.podman)
	r.podmanFile = knownName{modified: info.ModTime(), size: info.Size()}
}

// podmanNames reads the containers' names from the list of containers of
// containers/storage, which Podman and CRI-O keep and only root may read: a
// JSON array of containers, each with its id and names.
func podmanNames(path string) map[string]string {
	data, err := os.ReadFile(path) //nolint:gosec // the fixed path of Podman's list
	if err != nil {
		return nil
	}
	var list []struct {
		ID    string   `json:"id"`
		Names []string `json:"names"`
	}
	if json.Unmarshal(data, &list) != nil {
		return nil
	}
	names := make(map[string]string, len(list))
	for _, c := range list {
		if len(c.Names) > 0 && c.Names[0] != "" {
			names[c.ID] = c.Names[0]
		}
	}
	return names
}

// find returns the cgroup of each container, by container id: from the
// whole cgroup tree every walkEvery reads, else from the folders that held
// containers at the last walk.
func (r *Reader) find() map[string]string {
	found := map[string]string{}
	if r.reads%walkEvery == 0 {
		findContainers(r.cgroups, 0, found)
		r.parents = r.parents[:0]
		for _, dir := range found {
			if parent := filepath.Dir(dir); !slices.Contains(r.parents, parent) {
				r.parents = append(r.parents, parent)
			}
		}
	} else {
		for _, dir := range r.parents {
			findIn(dir, found)
		}
	}
	r.reads++
	return found
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
	for _, path := range findIn(dir, found) {
		findContainers(path, depth+1, found)
	}
}

// findIn adds the cgroup of each container right in dir to found, by
// container id, and returns the other folders in it.
func findIn(dir string, found map[string]string) (others []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(dir, name)
		if m := scope.FindStringSubmatch(name); m != nil {
			found[m[1]] = path
		} else if bareID.MatchString(name) {
			found[name] = path
		} else {
			others = append(others, path)
		}
	}
	return others
}

// anyDocker reports whether one of the containers found is Docker's.
func anyDocker(found map[string]string) bool {
	for _, dir := range found {
		if isDocker(dir) {
			return true
		}
	}
	return false
}

// isDocker reports whether a container is Docker's, by its cgroup folder:
// docker-<id>.scope, or <id> in a folder named docker.
func isDocker(dir string) bool {
	return strings.HasPrefix(filepath.Base(dir), "docker-") || filepath.Base(filepath.Dir(dir)) == "docker"
}

// dockerName reads a Docker container's name from its settings, path, which
// is <Docker's folder>/<id>/config.v2.json and which only root may read. It
// is "" when they cannot be read, such as for a Podman container.
func dockerName(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // Docker's folder and an id of 64 hexadecimal digits
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
// their memory, each value under the container's itemID.
func Extras(containers []Container) []metrics.Extra {
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
	used := map[string]bool{}
	for _, c := range containers {
		id := itemID(c)
		if used[id] {
			id = c.ID[:12]
		}
		used[id] = true
		if c.CPU != nil {
			cpu.Items = append(cpu.Items, metrics.ExtraItem{
				ID: id, Label: c.Name, Unit: metrics.UnitPercent, Value: c.CPU, History: true,
			})
		}
		if c.MemoryBytes != nil {
			bytes := float64(*c.MemoryBytes)
			memory.Items = append(memory.Items, metrics.ExtraItem{
				ID: id, Label: c.Name, Unit: metrics.UnitBytes, Value: &bytes, History: true,
			})
		}
	}
	var extras []metrics.Extra
	for _, group := range []metrics.Extra{cpu, memory} {
		if len(group.Items) > 0 {
			extras = append(extras, group)
		}
	}
	return extras
}

// notInID matches what an id may not hold.
var notInID = regexp.MustCompile(`[^a-z0-9_-]+`)

// itemID names a container's values: by its name where that is known, so
// its history goes on when the container is made anew, as by docker compose
// up after a pull, else by its short id. A name that has to be changed or cut
// to fit an id, at most 40 lowercase letters, digits, "_" and "-", ends in a
// checksum of the name, so two such names stay apart.
func itemID(c Container) string {
	short := c.ID[:12]
	if c.Name == short {
		return short
	}
	id := strings.Trim(notInID.ReplaceAllString(strings.ToLower(c.Name), "-"), "-_")
	if id == c.Name && len(id) <= 40 {
		return id
	}
	sum := fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(c.Name)))
	if id = strings.TrimRight(id[:min(len(id), 31)], "-_"); id == "" {
		return sum
	}
	return id + "-" + sum
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

// HostSys returns where /sys is: HOST_SYS in a container that mounts the
// host's /sys there, else /sys.
func HostSys() string {
	if dir := os.Getenv("HOST_SYS"); dir != "" {
		return dir
	}
	return "/sys"
}

// DockerDir returns Docker's folder of container settings: the folder
// containers in DOCKER_DIR, Docker's data folder, which is /var/lib/docker
// unless Docker is set up to keep its data elsewhere.
func DockerDir() string {
	dir := os.Getenv("DOCKER_DIR")
	if dir == "" {
		dir = "/var/lib/docker"
	}
	return filepath.Join(dir, "containers")
}
