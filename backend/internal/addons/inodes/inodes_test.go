package inodes

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

const table = `/dev/mmcblk0p2 / ext4 rw,noatime 0 0
proc /proc proc rw,relatime 0 0
sysfs /sys sysfs rw,relatime 0 0
tmpfs /run tmpfs rw,nosuid,nodev 0 0
cgroup2 /sys/fs/cgroup cgroup2 rw 0 0
/dev/mmcblk0p1 /boot/firmware vfat rw,relatime 0 0
/dev/sda1 /mnt/usb\040disk\134x ext4 rw,relatime 0 0
/dev/mmcblk0p2 /var/lib/docker/bind ext4 rw,noatime 0 0
/dev/loop3 /snap/core22/1380 squashfs ro 0 0
overlay /var/lib/docker/overlay2/abc/merged overlay rw 0 0
nas:/export /mnt/nas nfs4 rw 0 0
sshfs#me@host: /mnt/remote fuse.sshfs rw 0 0
/dev/sdb1 /mnt/windows fuseblk rw 0 0
systemd-1 /mnt/auto autofs rw 0 0
tank/data /tank/data zfs rw 0 0
/dev/sdc1 /boot/firmware vfat rw 0 0
tank/docker/4f1c /var/lib/docker/zfs/graph/4f1c zfs rw 0 0
default/containers/web /var/snap/lxd/common/lxd/storage-pools/default/containers/web zfs rw 0 0
tank/incus/c1 /var/lib/incus/storage-pools/tank/containers/c1 zfs rw 0 0
/dev/sdd1 /var/lib/docker ext4 rw 0 0
/dev/sda1 /srv/usb ext4 rw 0 0
/dev/sde1 /var/lib/docker/volumes ext4 rw 0 0
/dev/mapper/docker-8:1-123-abc /var/lib/docker/devicemapper/mnt/abc xfs rw 0 0
/dev/sdf1 /srv/docker ext4 rw 0 0
tank/docker/9e2a /srv/docker/zfs/graph/9e2a zfs rw 0 0
/dev/sdg1 /mnt/hidden ext4 rw 0 0
tmpfs /mnt/hidden tmpfs rw 0 0
/dev/mapper/vg-pool /var/lib/incus/storage-pools/default ext4 rw 0 0
/dev/mapper/vg-containers_c1 /var/lib/incus/storage-pools/default/containers/c1 ext4 rw 0 0
/dev/rbd0 /var/lib/lxd/storage-pools/ceph/virtual-machines/vm1 ext4 rw 0 0
/dev/zd16 /var/snap/lxd/common/lxd/storage-pools/zfs/custom/default_data ext4 rw 0 0
`

func TestParseMountsKeepsRealFilesystems(t *testing.T) {
	got := ParseMounts(table, "/srv/docker/")

	// /boot/firmware is the top of the two filesystems mounted there, the
	// one statfs sees, and /mnt/hidden is left out as its top one is tmpfs.
	// Disks below Docker's folders are kept, its layers are not; of a
	// storage pool only its own mount is kept, not the containers' volumes.
	want := []Mount{
		{Source: "/dev/mmcblk0p2", Path: "/", Type: "ext4"},
		{Source: "/dev/sdc1", Path: "/boot/firmware", Type: "vfat"},
		{Source: "/dev/sda1", Path: "/mnt/usb disk\\x", Type: "ext4"},
		{Source: "/dev/mmcblk0p2", Path: "/var/lib/docker/bind", Type: "ext4"},
		{Source: "/dev/sdb1", Path: "/mnt/windows", Type: "fuseblk"},
		{Source: "tank/data", Path: "/tank/data", Type: "zfs"},
		{Source: "/dev/sdd1", Path: "/var/lib/docker", Type: "ext4"},
		{Source: "/dev/sda1", Path: "/srv/usb", Type: "ext4"},
		{Source: "/dev/sde1", Path: "/var/lib/docker/volumes", Type: "ext4"},
		{Source: "/dev/sdf1", Path: "/srv/docker", Type: "ext4"},
		{Source: "/dev/mapper/vg-pool", Path: "/var/lib/incus/storage-pools/default", Type: "ext4"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseMounts() = %+v, want %+v", got, want)
	}
}

func TestReadReportsTheTopOfFilesystemsMountedOverEachOther(t *testing.T) {
	file := filepath.Join(t.TempDir(), "mounts")
	// /dev/sdb1 at /mnt/data is hidden by /dev/sdc1 on top of it, so its
	// mount at /mnt/other is the one to read, and /dev/sdc1's second mount
	// at /mnt/again is the one to leave out.
	table := "/dev/sdb1 /mnt/data ext4 rw 0 0\n/dev/sdc1 /mnt/data ext4 rw 0 0\n" +
		"/dev/sdb1 /mnt/other ext4 rw 0 0\n/dev/sdc1 /mnt/again ext4 rw 0 0\n"
	if err := os.WriteFile(file, []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	statfs := func(string) (uint64, uint64, bool) { return 1000, 250, true }

	got := newReader(file, statfs, time.Second).Read()

	want := []Usage{{Path: "/mnt/data", UsedPercent: 75}, {Path: "/mnt/other", UsedPercent: 75}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Read() = %+v, want %+v", got, want)
	}
}

func TestUsedPercentLeavesOutFilesystemsWithoutInodes(t *testing.T) {
	if got, ok := UsedPercent(1000, 250); !ok || got != 75 {
		t.Errorf("UsedPercent(1000, 250) = %v, %v; want 75", got, ok)
	}
	if _, ok := UsedPercent(0, 0); ok {
		t.Error("UsedPercent(0, 0) is ok, want false for btrfs and vfat")
	}
	if _, ok := UsedPercent(10, 20); ok {
		t.Error("UsedPercent(10, 20) is ok, want false for more free than total")
	}
}

func TestReadWithoutMountTable(t *testing.T) {
	if got := NewReader(t.TempDir() + "/missing").Read(); got != nil {
		t.Errorf("Read() of a missing table = %+v, want nothing", got)
	}
}

func TestReadLeavesOutAFilesystemThatHangs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "mounts")
	if err := os.WriteFile(file, []byte("/dev/sda2 / ext4 rw 0 0\n/dev/sdb1 /mnt/usb ext4 rw 0 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var mu sync.Mutex
	calls := map[string]int{}
	statfs := func(path string) (uint64, uint64, bool) {
		mu.Lock()
		calls[path]++
		mu.Unlock()
		if path == "/mnt/usb" {
			<-release
		}
		return 1000, 250, true
	}
	r := newReader(file, statfs, 20*time.Millisecond)

	for range 3 {
		if got, want := r.Read(), []Usage{{Path: "/", UsedPercent: 75}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("Read() while /mnt/usb hangs = %+v, want %+v", got, want)
		}
	}
	mu.Lock()
	if calls["/mnt/usb"] != 1 || calls["/"] != 3 {
		t.Errorf("statfs calls = %v, want /mnt/usb asked once while it hangs", calls)
	}
	mu.Unlock()

	// Once the stuck statfs returns, the filesystem is asked again.
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for len(r.Read()) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("Read() leaves out /mnt/usb after its statfs returned")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReadReportsADeviceAtItsFirstMountThatCanBeRead(t *testing.T) {
	file := filepath.Join(t.TempDir(), "mounts")
	table := "/dev/sdb1 /mnt/a ext4 rw 0 0\n/dev/sdb1 /mnt/b ext4 rw 0 0\n/dev/sdb1 /mnt/c ext4 rw 0 0\n"
	if err := os.WriteFile(file, []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	statfs := func(path string) (uint64, uint64, bool) {
		return 1000, 250, path != "/mnt/a"
	}

	got := newReader(file, statfs, time.Second).Read()

	if want := []Usage{{Path: "/mnt/b", UsedPercent: 75}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Read() = %+v, want %+v", got, want)
	}
}

func TestExtrasNameEachMountPoint(t *testing.T) {
	got := Extras([]Usage{
		{Path: "/", UsedPercent: 12.5},
		{Path: "/a-b", UsedPercent: 1},
		{Path: "/a/b", UsedPercent: 2},
	})

	if len(got) != 1 || got[0].ID != "inodes" || len(got[0].Items) != 3 {
		t.Fatalf("Extras() = %+v, want one inodes group with three values", got)
	}
	var ids []string
	for _, item := range got[0].Items {
		ids = append(ids, item.ID)
		if item.Unit != "percent" || !item.History || item.Label == "" {
			t.Errorf("item = %+v, want a percent with history and a label", item)
		}
	}
	if want := []string{"root", "a-b-13969bf8", "a-b"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	if got[0].Items[0].Label != "/" || *got[0].Items[0].Value != 12.5 {
		t.Errorf("first item = %+v, want / at 12.5%%", got[0].Items[0])
	}
	if Extras(nil) != nil {
		t.Error("Extras(nil) is not nil, want no group without filesystems")
	}
}

func TestIDsDependOnTheMountPointAlone(t *testing.T) {
	// /a-b-2 is what /a-b would have been called next to /a/b by a counter.
	paths := []string{"/a/b", "/a-b", "/a-b-2", "/root", "/mnt/USB", "/_", "/"}
	ids := map[string]string{}
	for _, path := range paths {
		ids[path] = idOf(path)
	}
	reversed := slices.Clone(paths)
	slices.Reverse(reversed)
	for _, order := range [][]string{paths, reversed} {
		var usages []Usage
		for _, path := range order {
			usages = append(usages, Usage{Path: path, UsedPercent: 1})
		}
		// CleanExtras is what usage-control keeps of an add-on's extras.
		items := metrics.CleanExtras(Extras(usages), 64)[0].Items
		if len(items) != len(paths) {
			t.Fatalf("CleanExtras() kept %d of %d values: %+v", len(items), len(paths), items)
		}
		for _, item := range items {
			if item.ID != ids[item.Label] {
				t.Errorf("%s is %q, want %q in any order", item.Label, item.ID, ids[item.Label])
			}
		}
	}
	want := map[string]string{
		"/a/b": "a-b", "/a-b": "a-b-13969bf8", "/a-b-2": "a-b-2-129c2b7f", "/root": "root-b203698f",
		"/mnt/USB": "mnt-usb-0adbd78b", "/_": "a81166f7", "/": "root",
	}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

func TestIDOfIsCut(t *testing.T) {
	long := "/srv/" + "abcdefghij" + "/abcdefghij" + "/abcdefghij" + "/abcdefghij"
	if got := idOf(long); got != "srv-abcdefghij-abcdefghij-abcde-4a731444" {
		t.Errorf("idOf(%q) = %q", long, got)
	}
	// Two long paths that only differ at the end must not become one id.
	if a, b := idOf(long), idOf(long[:len(long)-1]+"k"); a == b || len(b) > 40 {
		t.Errorf("idOf() = %q and %q, want two different ids of at most 40 characters", a, b)
	}
}
