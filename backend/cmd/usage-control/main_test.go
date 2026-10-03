package main

import (
	"os"
	"reflect"
	"testing"
	"time"
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
