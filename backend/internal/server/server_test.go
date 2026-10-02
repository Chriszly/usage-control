package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

type fakeCollector struct {
	snapshot metrics.Snapshot
	err      error
}

func (f fakeCollector) Collect(context.Context) (metrics.Snapshot, error) {
	return f.snapshot, f.err
}

var site = fstest.MapFS{
	"en/index.html":        {Data: []byte("<h1>Usage Control</h1>")},
	"de/index.html":        {Data: []byte("<h1>Usage Control</h1>")},
	"fr/index.html":        {Data: []byte("<h1>Usage Control</h1>")},
	"3rdpartylicenses.txt": {Data: []byte("MIT")},
}

func get(handler http.Handler, path, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestMetricsReturnsSnapshotAsJSON(t *testing.T) {
	want := metrics.Snapshot{CPU: metrics.CPU{UsagePercent: 12.5, Cores: 4}}
	handler := New(fakeCollector{snapshot: want}, site)

	rec := get(handler, "/api/metrics", "192.168.1.20:5000")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got metrics.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.CPU != want.CPU {
		t.Errorf("CPU = %+v, want %+v", got.CPU, want.CPU)
	}
}

func TestMetricsReportsCollectorError(t *testing.T) {
	handler := New(fakeCollector{err: errors.New("no /proc")}, site)

	rec := get(handler, "/api/metrics", "127.0.0.1:5000")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestServesWebsite(t *testing.T) {
	handler := New(fakeCollector{}, site)

	rec := get(handler, "/de/", "10.0.0.5:5000")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestExplainsMissingWebsiteBuild(t *testing.T) {
	handler := New(fakeCollector{}, fstest.MapFS{})

	rec := get(handler, "/", "10.0.0.5:5000")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestLocalNetworkOnly(t *testing.T) {
	tests := []struct {
		remoteAddr string
		wantStatus int
	}{
		{"127.0.0.1:5000", http.StatusOK},
		{"[::1]:5000", http.StatusOK},
		{"192.168.1.20:5000", http.StatusOK},
		{"10.1.2.3:5000", http.StatusOK},
		{"172.16.0.9:5000", http.StatusOK},
		{"169.254.10.10:5000", http.StatusOK},
		{"[fd00::1]:5000", http.StatusOK},
		{"[fe80::1]:5000", http.StatusOK},
		{"[::ffff:192.168.1.20]:5000", http.StatusOK},
		{"8.8.8.8:5000", http.StatusForbidden},
		{"172.32.0.1:5000", http.StatusForbidden},
		{"[2001:db8::1]:5000", http.StatusForbidden},
		{"not-an-address", http.StatusForbidden},
	}
	handler := New(fakeCollector{}, site)

	for _, tt := range tests {
		t.Run(tt.remoteAddr, func(t *testing.T) {
			rec := get(handler, "/api/metrics", tt.remoteAddr)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestOpensWebsiteInPreferredLanguage(t *testing.T) {
	tests := []struct {
		name           string
		acceptLanguage string
		cookie         string
		want           string
	}{
		{"no preference", "", "", "/en/"},
		{"browser language", "de-DE,de;q=0.9,en;q=0.8", "", "/de/"},
		{"first available by quality", "it, fr;q=0.5, de;q=0.7", "", "/de/"},
		{"no translation", "it, ja;q=0.5", "", "/en/"},
		{"picked in the switcher", "de", "fr", "/fr/"},
		{"unknown picked language", "de", "../etc", "/de/"},
	}
	handler := New(fakeCollector{}, site)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "192.168.1.20:5000"
			if tt.acceptLanguage != "" {
				req.Header.Set("Accept-Language", tt.acceptLanguage)
			}
			if tt.cookie != "" {
				req.Header.Set("Cookie", languageCookie+"="+tt.cookie)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
			}
			if got := rec.Header().Get("Location"); got != tt.want {
				t.Errorf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAcceptedLanguages(t *testing.T) {
	tests := []struct {
		header string
		want   []string
	}{
		{"", []string{}},
		{"de-CH, fr;q=0.8, en;q=0.5", []string{"de", "fr", "en"}},
		{"en;q=0.5, ES;q=0.9", []string{"es", "en"}},
		{"fr;q=0, de", []string{"de"}},
		{"*, de;q=0.1", []string{"de"}},
		{"de;q=abc, fr", []string{"fr"}},
	}
	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			if got := acceptedLanguages(tt.header); !slices.Equal(got, tt.want) {
				t.Errorf("acceptedLanguages(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}
