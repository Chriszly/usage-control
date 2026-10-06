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
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// maxMounts is the most filesystems reported, as many values as a group of
// extras keeps.
const maxMounts = 64

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

// ParseMounts reads a mount table in the format of /proc/self/mounts and
// returns the real filesystems, each once: a filesystem mounted at several
// paths, such as through a bind mount, keeps the first. Filesystems in user
// space ("fuse.sshfs" and the like) are left out like network filesystems,
// except fuseblk, a disk such as an NTFS one.
func ParseMounts(table string) []Mount {
	var mounts []Mount
	seen := map[string]bool{}
	for line := range strings.Lines(table) {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		m := Mount{Source: unescape(fields[0]), Path: unescape(fields[1]), Type: fields[2]}
		if skipped[m.Type] || strings.HasPrefix(m.Type, "fuse.") || !strings.HasPrefix(m.Path, "/") {
			continue
		}
		if strings.HasPrefix(m.Source, "/") {
			if seen[m.Source] {
				continue
			}
			seen[m.Source] = true
		}
		mounts = append(mounts, m)
	}
	return mounts
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

// Read returns the inode usage of each real filesystem in the mount table
// file, in its order, or nothing when it cannot be read.
func Read(file string) []Usage {
	table, err := os.ReadFile(file) //nolint:gosec // the kernel's mount table, at a fixed path
	if err != nil {
		return nil
	}
	var usages []Usage
	for _, m := range ParseMounts(string(table)) {
		total, free, ok := statInodes(m.Path)
		if !ok {
			continue
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

var notInID = regexp.MustCompile(`[^a-z0-9]+`)

// idOf turns a mount point into an id for an extra: "/" is "root" and
// "/boot/firmware" is "boot-firmware", cut to 40 characters.
func idOf(path string) string {
	id := strings.Trim(notInID.ReplaceAllString(strings.ToLower(path), "-"), "-")
	if len(id) > 40 {
		id = strings.TrimRight(id[:40], "-")
	}
	if id == "" {
		return "root"
	}
	return id
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
	ids := map[string]int{}
	for _, u := range usages {
		id := idOf(u.Path)
		// Two paths can make the same id, such as /a-b and /a/b.
		if ids[id]++; ids[id] > 1 {
			suffix := "-" + strconv.Itoa(ids[id])
			id = strings.TrimRight(id[:min(len(id), 40-len(suffix))], "-") + suffix
		}
		percent := u.UsedPercent
		group.Items = append(group.Items, metrics.ExtraItem{
			ID:      id,
			Label:   u.Path,
			Unit:    metrics.UnitPercent,
			Value:   &percent,
			History: true,
		})
	}
	return []metrics.Extra{group}
}
