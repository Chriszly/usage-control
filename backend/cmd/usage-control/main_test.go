package main

import (
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
		got, err := dataOnly()
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("dataOnly() with DATA_ONLY=%q = %v, %v, want %v with error %v", tt.value, got, err, tt.want, tt.wantErr)
		}
	}
}
