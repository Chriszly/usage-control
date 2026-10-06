package inodes

import (
	"reflect"
	"testing"
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
`

func TestParseMountsKeepsRealFilesystemsOnce(t *testing.T) {
	got := ParseMounts(table)

	want := []Mount{
		{Source: "/dev/mmcblk0p2", Path: "/", Type: "ext4"},
		{Source: "/dev/mmcblk0p1", Path: "/boot/firmware", Type: "vfat"},
		{Source: "/dev/sda1", Path: "/mnt/usb disk\\x", Type: "ext4"},
		{Source: "/dev/sdb1", Path: "/mnt/windows", Type: "fuseblk"},
		{Source: "tank/data", Path: "/tank/data", Type: "zfs"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseMounts() = %+v, want %+v", got, want)
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
	if got := Read(t.TempDir() + "/missing"); got != nil {
		t.Errorf("Read() of a missing table = %+v, want nothing", got)
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
	if want := []string{"root", "a-b", "a-b-2"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	if got[0].Items[0].Label != "/" || *got[0].Items[0].Value != 12.5 {
		t.Errorf("first item = %+v, want / at 12.5%%", got[0].Items[0])
	}
	if Extras(nil) != nil {
		t.Error("Extras(nil) is not nil, want no group without filesystems")
	}
}

func TestIDOfIsCut(t *testing.T) {
	long := "/srv/" + "abcdefghij" + "/abcdefghij" + "/abcdefghij" + "/abcdefghij"
	if got := idOf(long); len(got) > 40 || got != "srv-abcdefghij-abcdefghij-abcdefghij-abc" {
		t.Errorf("idOf(%q) = %q", long, got)
	}
}
