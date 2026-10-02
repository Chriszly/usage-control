package metrics

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestThroughputMeasuresSpeedSincePreviousReading(t *testing.T) {
	previous := map[string]counters{
		"eth0":  {received: 1000, sent: 500},
		"wlan0": {received: 9000, sent: 9000},
	}
	current := map[string]counters{
		"eth0":  {received: 5000, sent: 1500},
		"wlan0": {received: 10, sent: 20},   // reset since the previous reading
		"usb0":  {received: 300, sent: 400}, // new since the previous reading
	}

	got := throughput(previous, current, 2*time.Second)

	want := []NetworkInterface{
		{Name: "eth0", ReceivedBytes: 5000, SentBytes: 1500, ReceiveBytesPerSecond: 2000, SendBytesPerSecond: 500},
		{Name: "usb0", ReceivedBytes: 300, SentBytes: 400},
		{Name: "wlan0", ReceivedBytes: 10, SentBytes: 20},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("throughput() = %+v, want %+v", got, want)
	}
}

func TestThroughputOfFirstReadingIsZero(t *testing.T) {
	got := throughput(nil, map[string]counters{"eth0": {received: 1000, sent: 500}}, time.Since(time.Time{}))

	want := []NetworkInterface{{Name: "eth0", ReceivedBytes: 1000, SentBytes: 500}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("throughput() = %+v, want %+v", got, want)
	}
}

func TestIsVirtualInterface(t *testing.T) {
	sysDir := t.TempDir()
	classNet := filepath.Join(sysDir, "class", "net")
	if err := os.MkdirAll(classNet, 0o750); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"eth0":    "../../devices/platform/scb/fd580000.ethernet/net/eth0",
		"docker0": "../../devices/virtual/net/docker0",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(classNet, name)); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		sysDir, name string
		want         bool
	}{
		{sysDir, "eth0", false},
		{sysDir, "docker0", true},
		{filepath.Join(sysDir, "missing"), "lo", true},
		{filepath.Join(sysDir, "missing"), "Ethernet", false},
	}
	for _, tt := range tests {
		if got := isVirtualInterface(tt.sysDir, tt.name); got != tt.want {
			t.Errorf("isVirtualInterface(%q, %q) = %v, want %v", tt.sysDir, tt.name, got, tt.want)
		}
	}
}

func TestLoopbackInterfacesFindsLoopback(t *testing.T) {
	if len(loopbackInterfaces()) == 0 {
		t.Error("loopbackInterfaces() is empty, want at least the loopback interface")
	}
}
