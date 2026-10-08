package metrics

import (
	"bytes"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHostAddressesListsTheHostsVirtualInterfacesToo(t *testing.T) {
	proc := t.TempDir()
	netDir := filepath.Join(proc, "1", "net")
	if err := os.MkdirAll(netDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// The host's card, Docker's bridges and a WireGuard tunnel.
	trie := `Main:
  +-- 0.0.0.0/0 3 0 5
     +-- 172.16.0.0/12 2 0 2
           |-- 172.17.0.1
              /32 host LOCAL
           |-- 172.18.0.1
              /32 host LOCAL
     +-- 192.168.60.0/24 2 0 2
           |-- 192.168.60.9
              /32 host LOCAL
        |-- 192.168.60.255
           /32 link BROADCAST
     +-- 10.8.0.0/24 2 0 2
           |-- 10.8.0.2
              /32 host LOCAL
`
	inet6 := "fd000000000000000000000000000009 02 40 00 80 eth0\nfe800000000000000000000000000001 03 40 20 80 docker0\n"
	for name, text := range map[string]string{"fib_trie": trie, "if_inet6": inet6} {
		if err := os.WriteFile(filepath.Join(netDir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("HOST_PROC", proc)
	var want []netip.Addr
	for _, a := range []string{"172.17.0.1", "172.18.0.1", "192.168.60.9", "10.8.0.2", "fd00::9", "fe80::1"} {
		want = append(want, netip.MustParseAddr(a))
	}
	if got := HostAddresses(); !reflect.DeepEqual(got, want) {
		t.Errorf("HostAddresses() = %v, want %v", got, want)
	}

	t.Setenv("HOST_PROC", "")
	if got := HostAddresses(); got != nil {
		t.Errorf("HostAddresses() outside a container = %v, want none", got)
	}
}

func TestHostAddressesSaysOnceThatTheyCannotBeRead(t *testing.T) {
	// HOST_PROC without the host's routing files, as with hidepid.
	t.Setenv("HOST_PROC", t.TempDir())
	hostAddressesUnread.Store(false)
	t.Cleanup(func() { hostAddressesUnread.Store(false) })
	var logged bytes.Buffer
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))

	for range 3 {
		if got := HostAddresses(); got != nil {
			t.Errorf("HostAddresses() = %v, want none", got)
		}
	}
	if n := strings.Count(logged.String(), "read the host's addresses"); n != 1 {
		t.Errorf("logged %d times, want once:\n%s", n, logged.String())
	}
}
