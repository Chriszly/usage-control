// Package inodes reads how many of each filesystem's inodes are in use, for
// the inodes add-on. Every file and folder takes one inode, and a filesystem
// with none left cannot hold a new file even when it has free space, such as
// a disk full of small cache or mail files.
//
// It lists the real filesystems in the mount table and asks each for its
// inode counts with statfs; it only reads, nothing in here changes the
// machine. Only Linux has inodes to report.
package inodes

import (
	"fmt"
	"hash/crc32"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

const (
	// maxMounts is the most filesystems reported, as many values as a group
	// of extras keeps.
	maxMounts = 64
	// statTimeout is how long statfs may take for one filesystem. A local
	// disk can stop answering too, such as a dying USB disk or a stuck
	// fuseblk helper, and statfs then waits for it for good.
	statTimeout = time.Second
)

// Mount is a filesystem from the mount table.
type Mount struct {
	// Source is the device or name it is mounted from, such as /dev/sda1.
	Source string
	// Path is where it is mounted, such as /boot/firmware.
	Path string
	// Type is its filesystem type, such as ext4.
	Type string
}

// Usage is the inode usage of one mounted filesystem.
type Usage struct {
	Path        string
	UsedPercent float64
}

// skipped are the filesystem types without inodes worth reporting: those
// that only show the kernel's state or live in memory, read-only images,
// and network filesystems, whose statfs can hang while the server is away.
// autofs is left out so the add-on does not mount what is only set to mount
// on use; the filesystem mounted there shows up on its own.
var skipped = map[string]bool{
	"autofs": true, "binfmt_misc": true, "bpf": true, "cgroup": true, "cgroup2": true,
	"configfs": true, "debugfs": true, "devpts": true, "devtmpfs": true, "efivarfs": true,
	"fusectl": true, "hugetlbfs": true, "mqueue": true, "nsfs": true, "overlay": true,
	"proc": true, "pstore": true, "ramfs": true, "rpc_pipefs": true, "securityfs": true,
	"selinuxfs": true, "squashfs": true, "sysfs": true, "tmpfs": true, "tracefs": true,
	"iso9660": true, "erofs": true, "zram": true,
	"nfs": true, "nfs4": true, "cifs": true, "smb3": true, "smbfs": true, "9p": true,
	"ceph": true, "glusterfs": true, "afs": true, "virtiofs": true,
}

// containerFolders are the folders of the container engines that keep a
// container's layers or root in them. What is mounted below them, other than
// a disk, is such a layer or root, such as one ZFS dataset per layer with
// Docker's zfs driver, and would fill the slots of the group with values
// that come and go with the containers. A disk mounted at such a folder
// itself, or below it, such as one for Docker's volumes, is kept.
var containerFolders = []string{
	"/var/lib/docker/",
	"/var/lib/containerd/",
	"/var/lib/containers/storage/",
}

// storagePools are the folders of LXD's and Incus's storage pools. Each pool
// is mounted at storage-pools/<pool>, which is kept, and below it each
// container, virtual machine, image and custom volume, which is left out
// whatever it is mounted from: with LVM, Ceph or ZFS volumes these are disks
// of their own, one per container.
var storagePools = []string{
	"/var/lib/lxd/storage-pools/",
	"/var/snap/lxd/common/lxd/storage-pools/",
	"/var/lib/incus/storage-pools/",
}

// belowContainerFolder reports whether path is below a container engine's
// folder, or below dockerDir, Docker's data folder where DOCKER_DIR moves it
// ("" when it does not).
func belowContainerFolder(path, dockerDir string) bool {
	if dockerDir = strings.TrimRight(dockerDir, "/"); dockerDir != "" && strings.HasPrefix(path, dockerDir+"/") {
		return true
	}
	for _, folder := range containerFolders {
		if strings.HasPrefix(path, folder) {
			return true
		}
	}
	return false
}

// belowStoragePool reports whether path is below an LXD or Incus storage
// pool's own mount point, as a container's volume is.
func belowStoragePool(path string) bool {
	for _, folder := range storagePools {
		if pool, ok := strings.CutPrefix(path, folder); ok && strings.Contains(strings.Trim(pool, "/"), "/") {
			return true
		}
	}
	return false
}

// containerLayer reports whether m is a container's own layer, root or
// volume: what is mounted below a storage pool, and what is mounted below
// another container engine's folder and is not a disk, or is a thin device of
// Docker's old devicemapper driver, a disk of its own per container.
func containerLayer(m Mount, dockerDir string) bool {
	if belowStoragePool(m.Path) {
		return true
	}
	if !belowContainerFolder(m.Path, dockerDir) {
		return false
	}
	return !strings.HasPrefix(m.Source, "/dev/") || strings.HasPrefix(m.Source, "/dev/mapper/docker-")
}

// ParseMounts reads a mount table in the format of /proc/self/mounts and
// returns the real filesystems. A path with filesystems mounted over each
// other is listed once, with the top one, the last in the table, which is
// the one statfs sees. A container's own layers below a container engine's
// folder are left out, with dockerDir, Docker's data folder where DOCKER_DIR
// moves it, as one more such folder. A filesystem mounted at several paths,
// such as through a bind mount, is listed at each; Read keeps the first that
// can be read. Filesystems in user space ("fuse.sshfs" and the like) are left
// out like network filesystems, except fuseblk, a disk such as an NTFS one.
func ParseMounts(table, dockerDir string) []Mount {
	var mounts []Mount
	// at is where each path is in mounts.
	at := map[string]int{}
	for line := range strings.Lines(table) {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		m := Mount{Source: unescape(fields[0]), Path: unescape(fields[1]), Type: fields[2]}
		if !strings.HasPrefix(m.Path, "/") {
			continue
		}
		if i, ok := at[m.Path]; ok {
			mounts[i] = m
			continue
		}
		at[m.Path] = len(mounts)
		mounts = append(mounts, m)
	}
	return slices.DeleteFunc(mounts, func(m Mount) bool {
		return skipped[m.Type] || strings.HasPrefix(m.Type, "fuse.") || containerLayer(m, dockerDir)
	})
}

// escaped matches a character the mount table writes as an octal escape,
// such as \040 for a space.
var escaped = regexp.MustCompile(`\\[0-7]{3}`)

func unescape(field string) string {
	return escaped.ReplaceAllStringFunc(field, func(code string) string {
		return string([]byte{(code[1]-'0')<<6 | (code[2]-'0')<<3 | (code[3] - '0')})
	})
}

// UsedPercent is how many of total inodes are in use when free are left. It
// is false for a filesystem that reports no inodes, such as btrfs or vfat,
// which make them as needed or have none.
func UsedPercent(total, free uint64) (float64, bool) {
	if total == 0 || free > total {
		return 0, false
	}
	return float64(total-free) / float64(total) * 100, true
}

// Reader reads the inode usage of the filesystems in a mount table. It is
// used by one goroutine at a time.
type Reader struct {
	table   string
	statfs  func(path string) (total, free uint64, ok bool)
	timeout time.Duration
	// dockerDir is Docker's data folder from DOCKER_DIR, or "".
	dockerDir string

	// mu guards asking, the mount points whose statfs has not returned yet.
	mu     sync.Mutex
	asking map[string]bool
	// hanging are the mount points whose statfs took too long, so that is
	// logged once and not at every read.
	hanging map[string]bool
}

// NewReader returns a Reader for the mount table file, such as
// /proc/self/mounts. DOCKER_DIR, where Docker keeps its data elsewhere, names
// one more container engine's folder.
func NewReader(table string) *Reader {
	r := newReader(table, statInodes, statTimeout)
	r.dockerDir = os.Getenv("DOCKER_DIR")
	return r
}

func newReader(table string, statfs func(string) (uint64, uint64, bool), timeout time.Duration) *Reader {
	return &Reader{table: table, statfs: statfs, timeout: timeout, asking: map[string]bool{}, hanging: map[string]bool{}}
}

// Read returns the inode usage of each real filesystem in the mount table,
// in its order, or nothing when the table cannot be read. A device mounted
// at several paths is reported at the first one that can be read, so one
// whose first mount does not answer still shows up.
func (r *Reader) Read() []Usage {
	table, err := os.ReadFile(r.table)
	if err != nil {
		return nil
	}
	var usages []Usage
	read := map[string]bool{}
	for _, m := range ParseMounts(string(table), r.dockerDir) {
		// Only a device is the same filesystem wherever it is mounted; a
		// name such as a ZFS dataset's is checked by its path alone.
		device := strings.HasPrefix(m.Source, "/")
		if device && read[m.Source] {
			continue
		}
		total, free, ok := r.stat(m.Path)
		if !ok {
			continue
		}
		if device {
			read[m.Source] = true
		}
		if percent, ok := UsedPercent(total, free); ok {
			usages = append(usages, Usage{Path: m.Path, UsedPercent: percent})
		}
		if len(usages) == maxMounts {
			break
		}
	}
	return usages
}

// stat asks statfs for the inodes of the filesystem at path and waits for
// it at most r.timeout. A filesystem that does not answer in time is left
// out, and is not asked again until the earlier statfs returns, so a disk
// that hangs holds one goroutine and not one more at every read.
func (r *Reader) stat(path string) (total, free uint64, ok bool) {
	r.mu.Lock()
	busy := r.asking[path]
	r.asking[path] = true
	r.mu.Unlock()
	if busy {
		return 0, 0, false
	}

	type answer struct {
		total, free uint64
		ok          bool
	}
	answers := make(chan answer, 1)
	go func() {
		var a answer
		a.total, a.free, a.ok = r.statfs(path)
		r.mu.Lock()
		delete(r.asking, path)
		r.mu.Unlock()
		answers <- a
	}()
	timeout := time.NewTimer(r.timeout)
	defer timeout.Stop()
	select {
	case a := <-answers:
		if r.hanging[path] {
			delete(r.hanging, path)
			slog.Info("the filesystem answers again", "path", path)
		}
		return a.total, a.free, a.ok
	case <-timeout.C:
		if !r.hanging[path] {
			r.hanging[path] = true
			slog.Warn("the filesystem does not answer, so its inodes are left out until it does", "path", path, "timeout", r.timeout)
		}
		return 0, 0, false
	}
}

var notInID = regexp.MustCompile(`[^a-z0-9]+`)

// idOf turns a mount point into an id for an extra that depends on the path
// alone, so each filesystem keeps its history however the others are
// mounted: "/" is "root" and "/boot/firmware" is "boot-firmware". An id
// that would not spell out the path, such as for /a-b, which /a/b has too,
// or one longer than 40 characters, is cut to 31 and ends in a checksum of
// the whole path.
func idOf(path string) string {
	if path == "/" {
		return "root"
	}
	id := strings.Trim(notInID.ReplaceAllString(strings.ToLower(path), "-"), "-")
	if id != "root" && len(id) <= 40 && "/"+strings.ReplaceAll(id, "-", "/") == path {
		return id
	}
	sum := fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(path)))
	if id = strings.TrimRight(id[:min(len(id), 31)], "-"); id == "" {
		return sum
	}
	return id + "-" + sum
}

// Extras returns the usages as the group of extras the collector shows,
// labelled by mount point.
func Extras(usages []Usage) []metrics.Extra {
	if len(usages) == 0 {
		return nil
	}
	group := metrics.Extra{
		ID:     "inodes",
		Title:  "Inodes (files) in use",
		Titles: map[string]string{"de": "Belegte Inodes (Dateien)", "fr": "Inodes (fichiers) utilisés", "es": "Inodos (archivos) en uso"},
	}
	for _, u := range usages {
		percent := u.UsedPercent
		group.Items = append(group.Items, metrics.ExtraItem{
			ID:      idOf(u.Path),
			Label:   u.Path,
			Unit:    metrics.UnitPercent,
			Value:   &percent,
			History: true,
		})
	}
	return []metrics.Extra{group}
}
