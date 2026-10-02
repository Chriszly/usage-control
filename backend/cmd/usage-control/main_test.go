package main

import (
	"reflect"
	"testing"
)

func TestDiskPaths(t *testing.T) {
	tests := []struct {
		value string
		want  []string
	}{
		{"", []string{"/"}},
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
