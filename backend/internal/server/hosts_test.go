package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKnownHostsAllows(t *testing.T) {
	hosts := knownHosts{hostname: "raspberrypi", allowed: []string{"pi.fritz.box", "usage.home.lan"}}
	tests := []struct {
		host string
		want bool
	}{
		{"192.168.1.9:9393", true},
		{"192.168.1.9", true},
		{"[fd00::9]:9393", true},
		{"[fd00::9]", true},
		{"localhost:9393", true},
		{"LocalHost", true},
		{"raspberrypi", true},
		{"RaspberryPi:9393", true},
		{"raspberrypi.local:9393", true},
		{"anything.local", true},
		{"pi.fritz.box:9393", true},
		{"PI.FRITZ.BOX.", true},
		{"usage.home.lan", true},
		{"", true}, // an HTTP/1.0 request without a Host header
		{"evil.example:9393", false},
		{"raspberrypi.evil.example", false},
		{"fritz.box", false},
		{"pi.fritz.box.evil.example", false},
		{"local", false},
		{"notlocal", false},
		{".local", false},
	}
	for _, tt := range tests {
		if got := hosts.allows(tt.host); got != tt.want {
			t.Errorf("allows(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestNewKnownHostsCleansTheAllowedNames(t *testing.T) {
	hosts := newKnownHosts([]string{" Pi.Fritz.Box:9393 ", "", "home.lan."})
	for _, name := range []string{"pi.fritz.box", "home.lan:9393"} {
		if !hosts.allows(name) {
			t.Errorf("allows(%q) = false, want true", name)
		}
	}
	if hosts.allows("") != true {
		t.Error("allows(\"\") = false, want true")
	}
	if hosts.hostname == "" {
		t.Error("hostname is empty, want the machine's name")
	}
}

func TestKnownHostsOnlyRefusesUnknownNames(t *testing.T) {
	handler := newHandler(device(fakeCollector{}, nil), 0, site)
	tests := []struct {
		host       string
		wantStatus int
	}{
		{"192.168.1.9:9393", http.StatusOK},
		{"raspberrypi.local", http.StatusOK},
		{"evil.example:9393", http.StatusMisdirectedRequest},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
			req.RemoteAddr = "192.168.1.20:5000"
			req.Host = tt.host
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestAnOutsideAddressIsRefusedBeforeTheHostIsChecked(t *testing.T) {
	handler := newHandler(device(fakeCollector{}, nil), 0, site)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	req.RemoteAddr = "8.8.8.8:5000"
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
