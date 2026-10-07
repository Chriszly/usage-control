package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadLinuxInterfaceStatsFallsBackOutsideAContainer(t *testing.T) {
	dev := func(name string) string {
		return "Inter-|   Receive                                                |  Transmit\n" +
			" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
			"  " + name + ": 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0\n"
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The first process's network, and this program's own.
	both := t.TempDir()
	write(filepath.Join(both, "1", "net", "dev"), dev("eth0"))
	write(filepath.Join(both, "net", "dev"), dev("veth0"))
	// The first process hidden, as with hidepid.
	hidden := t.TempDir()
	write(filepath.Join(hidden, "net", "dev"), dev("veth0"))

	for _, test := range []struct {
		name        string
		procDir     string
		inContainer bool
		want        string
	}{
		{"first process", both, false, "eth0"},
		{"first process in a container", both, true, "eth0"},
		{"own network outside a container", hidden, false, "veth0"},
		{"nothing in a container", hidden, true, ""},
	} {
		stats, err := readLinuxInterfaceStats(t.Context(), test.procDir, test.inContainer)
		if test.want == "" {
			if err == nil {
				t.Errorf("%s: readLinuxInterfaceStats() = %+v, want an error", test.name, stats)
			}
			continue
		}
		if err != nil || len(stats) != 1 || stats[0].Name != test.want || stats[0].BytesRecv != 1000 {
			t.Errorf("%s: readLinuxInterfaceStats() = %+v, %v, want %s", test.name, stats, err, test.want)
		}
	}
}
