package ports

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeProc builds a /proc with the given files.
func fakeProc(t *testing.T, files map[string]string) string {
	t.Helper()
	proc := t.TempDir()
	for name, text := range files {
		path := filepath.Join(proc, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return proc
}

// A DNS lookup's socket, bound to a port in the ephemeral range and not
// connected, next to mDNS on 5353.
const procNetUDPWithLookup = procNetUDP +
	`  902: 00000000:9C41 00000000:0000 07 00000000:00000000 00:00000000 00000000  1000        0 61003 2 0000000000000000 0
`

func TestReadLeavesOutUDPPortsInTheEphemeralRange(t *testing.T) {
	// 0x9C41 is 40001: in Linux's default range, and outside the one set.
	tests := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"the default range", map[string]string{}, false},
		{"a range that was set", map[string]string{"sys/net/ipv4/ip_local_port_range": "50000\t60000\n"}, true},
	}
	for _, test := range tests {
		test.files["1/net/udp"] = procNetUDPWithLookup
		got, err := read(context.Background(), fakeProc(t, test.files))
		if err != nil {
			t.Fatal(err)
		}
		var numbers []uint32
		for _, p := range got {
			numbers = append(numbers, p.Number)
		}
		want := []uint32{53, 5353}
		if test.want {
			want = append(want, 40001)
		}
		if !reflect.DeepEqual(numbers, want) {
			t.Errorf("%s: ports %v, want %v", test.name, numbers, want)
		}
	}
}

func TestReaderLogsOnceWhenTheTablesCannotBeRead(t *testing.T) {
	var logged bytes.Buffer
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))

	proc := t.TempDir()
	r := NewReader(proc)
	for range 3 {
		if ports, ok := r.Read(context.Background()); ok || ports != nil {
			t.Fatalf("Read() without tables = %v, %v, want nothing", ports, ok)
		}
	}
	if n := strings.Count(logged.String(), "read the ports"); n != 1 {
		t.Errorf("logged the failure %d times, want once:\n%s", n, logged.String())
	}

	if err := os.MkdirAll(filepath.Join(proc, "1", "net"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "1", "net", "tcp"), []byte(procNetTCP), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if ports, ok := r.Read(context.Background()); !ok || len(ports) != 2 {
			t.Fatalf("Read() = %v, %v, want the two TCP ports", ports, ok)
		}
	}
	if n := strings.Count(logged.String(), "works again"); n != 1 {
		t.Errorf("logged that it works again %d times, want once:\n%s", n, logged.String())
	}
}
