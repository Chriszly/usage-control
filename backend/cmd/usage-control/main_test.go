package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func TestDiskPaths(t *testing.T) {
	tests := []struct {
		value string
		want  []string
	}{
		{"", []string{systemDisk()}},
		{"/", []string{"/"}},
		{" /, /mnt/usb ,", []string{"/", "/mnt/usb"}},
	}
	for _, tt := range tests {
		t.Setenv("DISK_PATHS", tt.value)
		if got := diskPaths(); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("diskPaths() with DISK_PATHS=%q = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestRetentionDays(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
	}{
		{"", 30 * 24 * time.Hour},
		{"7", 7 * 24 * time.Hour},
		{" 90 ", 90 * 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Setenv("RETENTION_DAYS", tt.value)
		got, err := retentionDays()
		if err != nil || got != tt.want {
			t.Errorf("retentionDays() with RETENTION_DAYS=%q = %v, %v, want %v", tt.value, got, err, tt.want)
		}
	}
}

func TestRetentionDaysRefusesInvalidValues(t *testing.T) {
	for _, value := range []string{"0", "-1", "30d", "1.5", "3651"} {
		t.Setenv("RETENTION_DAYS", value)
		if _, err := retentionDays(); err == nil {
			t.Errorf("retentionDays() with RETENTION_DAYS=%q error = nil, want an error", value)
		}
	}
}

func TestPagePort(t *testing.T) {
	tests := []struct {
		public, listen, want string
	}{
		{"", ":9393", "9393"},
		{"", "0.0.0.0:8080", "8080"},
		{"8090", ":9393", "8090"},
		{" 8090 ", ":9393", "8090"},
	}
	for _, tt := range tests {
		t.Setenv("PUBLIC_PORT", tt.public)
		got, err := pagePort(tt.listen)
		if err != nil || got != tt.want {
			t.Errorf("pagePort(%q) with PUBLIC_PORT=%q = %q, %v, want %q", tt.listen, tt.public, got, err, tt.want)
		}
	}
}

func TestPagePortRefusesInvalidValues(t *testing.T) {
	for _, value := range []string{"0", "65536", "port", "80/x"} {
		t.Setenv("PUBLIC_PORT", value)
		if _, err := pagePort(":9393"); err == nil {
			t.Errorf("pagePort() with PUBLIC_PORT=%q error = nil, want an error", value)
		}
	}
	t.Setenv("PUBLIC_PORT", "")
	if _, err := pagePort("9393"); err == nil {
		t.Error("pagePort(\"9393\") error = nil, want an error for a LISTEN_ADDR without a port")
	}
}

func TestHistoryMaxEntries(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{"", 64},
		{"1", 1},
		{" 200 ", 200},
	}
	for _, tt := range tests {
		t.Setenv("HISTORY_MAX_ENTRIES", tt.value)
		got, err := historyMaxEntries()
		if err != nil || got != tt.want {
			t.Errorf("historyMaxEntries() with HISTORY_MAX_ENTRIES=%q = %v, %v, want %v", tt.value, got, err, tt.want)
		}
	}
	for _, value := range []string{"0", "-1", "many", "1.5", "10001"} {
		t.Setenv("HISTORY_MAX_ENTRIES", value)
		if _, err := historyMaxEntries(); err == nil {
			t.Errorf("historyMaxEntries() with HISTORY_MAX_ENTRIES=%q error = nil, want an error", value)
		}
	}
}

func TestDataOnly(t *testing.T) {
	tests := []struct {
		value   string
		want    bool
		wantErr bool
	}{
		{"", false, false},
		{"true", true, false},
		{" false ", false, false},
		{"yes", false, true},
	}
	for _, tt := range tests {
		t.Setenv("DATA_ONLY", tt.value)
		got, err := boolSettingOr("DATA_ONLY", false)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("boolSettingOr(DATA_ONLY=%q) = %v, %v, want %v with error %v", tt.value, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestBoolSettingOr(t *testing.T) {
	tests := []struct {
		value    string
		fallback bool
		want     bool
	}{
		{"", true, true},
		{"", false, false},
		{"false", true, false},
		{" TRUE ", false, true},
	}
	for _, tt := range tests {
		t.Setenv("UPDATE_CHECK", tt.value)
		got, err := boolSettingOr("UPDATE_CHECK", tt.fallback)
		if err != nil || got != tt.want {
			t.Errorf("boolSettingOr() with %q and default %v = %v, %v, want %v", tt.value, tt.fallback, got, err, tt.want)
		}
	}
	t.Setenv("UPDATE_CHECK", "sometimes")
	if _, err := boolSettingOr("UPDATE_CHECK", true); err == nil {
		t.Error("boolSettingOr() with \"sometimes\" error = nil, want one")
	}
}

func TestOwnName(t *testing.T) {
	hostname, _ := os.Hostname()
	tests := []struct {
		deviceName, hostProc, want string
	}{
		{"Office PC", "", "Office PC"},
		{" Office PC ", "/host/proc", "Office PC"},
		{"", "", hostname},
		{"", "/host/proc", ""}, // in a container, the hostname is the container's
	}
	for _, tt := range tests {
		t.Setenv("DEVICE_NAME", tt.deviceName)
		t.Setenv("HOST_PROC", tt.hostProc)
		if got := ownName(); got != tt.want {
			t.Errorf("ownName() with DEVICE_NAME=%q, HOST_PROC=%q = %q, want %q", tt.deviceName, tt.hostProc, got, tt.want)
		}
	}
}

func TestBufferHours(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
	}{
		{"", 24 * time.Hour},
		{"1", time.Hour},
		{" 168 ", 7 * 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Setenv("BUFFER_HOURS", tt.value)
		got, err := bufferHours()
		if err != nil || got != tt.want {
			t.Errorf("bufferHours() with BUFFER_HOURS=%q = %v, %v, want %v", tt.value, got, err, tt.want)
		}
	}
}

func TestBufferHoursRefusesInvalidValues(t *testing.T) {
	for _, value := range []string{"0", "-1", "24h", "1.5", "169"} {
		t.Setenv("BUFFER_HOURS", value)
		if _, err := bufferHours(); err == nil {
			t.Errorf("bufferHours() with BUFFER_HOURS=%q error = nil, want an error", value)
		}
	}
}

func TestWithBufferKeepsTheMinutesInTheDatabase(t *testing.T) {
	collector, err := metrics.NewCollector(context.Background(), []string{t.TempDir()})
	if err != nil {
		t.Fatalf("NewCollector() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "usage-control.db")

	minutes, wait := withBuffer(ctx, metrics.NewSampler(collector), path, time.Hour, 1)
	cancel()
	wait()

	if _, err := os.Stat(path); minutes == nil || err != nil {
		t.Errorf("withBuffer() = %v, and the database %v; want the buffer, kept in the database", minutes, err)
	}
}

func TestWithBufferRunsWithoutADatabaseItCannotOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "usage-control.db")

	minutes, wait := withBuffer(context.Background(), nil, path, time.Hour, 1)
	wait()

	if minutes != nil {
		t.Errorf("withBuffer() with a database it cannot open = %v, want no minutes instead of failing", minutes)
	}
}

// setUpRun points run at a database in a temporary folder and clears the
// settings that would change what it sets up.
func setUpRun(t *testing.T, addr string) string {
	t.Helper()
	database := filepath.Join(t.TempDir(), "usage-control.db")
	t.Setenv("LISTEN_ADDR", addr)
	t.Setenv("DATABASE_PATH", database)
	t.Setenv("DISK_PATHS", t.TempDir())
	for _, name := range []string{"PUBLIC_PORT", "RETENTION_DAYS", "HISTORY_MAX_ENTRIES", "HUB_DEVICES", "DATA_ONLY", "BUFFER_HOURS", "RESET_PASSWORD", "UPDATE_CHECK", "ADDONS_DIR", "ALLOWED_HOSTS"} {
		t.Setenv(name, "")
	}
	return database
}

func TestRunStopsBeforeTheSetupWhenThePortIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = taken.Close() }()
	database := setUpRun(t, taken.Addr().String())

	err = run(context.Background())

	var opErr *net.OpError
	if !errors.As(err, &opErr) || opErr.Op != "listen" {
		t.Errorf("run() with a taken port error = %v, want the listen error", err)
	}
	if _, statErr := os.Stat(database); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("run() with a taken port opened the database (%v); it should stop before the setup", statErr)
	}
}

func TestRunStopsBeforeTheSetupWhenUpdateCheckIsInvalid(t *testing.T) {
	database := setUpRun(t, "127.0.0.1:0")
	t.Setenv("UPDATE_CHECK", "no")

	if err := run(context.Background()); err == nil || !strings.Contains(err.Error(), "UPDATE_CHECK") {
		t.Errorf("run() with UPDATE_CHECK=no error = %v, want one naming UPDATE_CHECK", err)
	}
	if _, statErr := os.Stat(database); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("run() with UPDATE_CHECK=no opened the database (%v); it should stop before the setup", statErr)
	}
}

func TestDiskPathsHintFitsThePlatform(t *testing.T) {
	tests := []struct {
		goos      string
		container bool
		want      string
	}{
		{"windows", false, `C:\`},
		{"linux", true, "compose.yaml"},
		{"linux", false, "/mnt/usb"},
		{"darwin", false, "/mnt/usb"},
	}
	for _, tt := range tests {
		if got := diskPathsHint(tt.goos, tt.container); !strings.Contains(got, tt.want) {
			t.Errorf("diskPathsHint(%q, %v) = %q, want it to mention %q", tt.goos, tt.container, got, tt.want)
		}
	}
}

func TestRunServesUntilStopped(t *testing.T) {
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	addr := free.Addr().String()
	_ = free.Close()
	setUpRun(t, addr)

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- run(ctx) }()

	answered := false
	for range 100 {
		if response, err := http.Get("http://" + addr + "/api/metrics"); err == nil {
			_ = response.Body.Close()
			answered = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-stopped; err != nil {
		t.Errorf("run() error = %v, want nil after it was stopped", err)
	}
	if !answered {
		t.Error("run() did not answer on its port")
	}
}

func TestRouterDevicesNeedThePasswordOfALogin(t *testing.T) {
	t.Setenv("HUB_ROUTERS", "Home router=upnp:192.168.1.1,Office=asus:admin@192.168.2.1")
	t.Setenv("CREDENTIALS_DIRECTORY", "")
	t.Setenv("ROUTER_PASSWORDS_FILE", "")
	if _, err := routerDevices(); err == nil || !strings.Contains(err.Error(), "Office") {
		t.Errorf("without a passwords file: %v, want an error naming the router", err)
	}

	path := filepath.Join(t.TempDir(), "router-passwords")
	if err := os.WriteFile(path, []byte("Home router=unused\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROUTER_PASSWORDS_FILE", path)
	if _, err := routerDevices(); err == nil || !strings.Contains(err.Error(), "Office") {
		t.Errorf("without the router's line: %v, want an error naming the router", err)
	}

	if err := os.WriteFile(path, []byte("office=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	devices, err := routerDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[0].ID != "home-router" || devices[1].ID != "office" || devices[1].Source == nil {
		t.Errorf("got %+v", devices)
	}
}
