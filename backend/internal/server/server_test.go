package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

type fakeCollector struct {
	snapshot metrics.Snapshot
	err      error
}

func (f fakeCollector) Collect(context.Context) (metrics.Snapshot, error) {
	return f.snapshot, f.err
}

var site = fstest.MapFS{"index.html": {Data: []byte("<h1>usage-control</h1>")}}

func get(handler http.Handler, path, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestMetricsReturnsSnapshotAsJSON(t *testing.T) {
	want := metrics.Snapshot{CPU: metrics.CPU{UsagePercent: 12.5, Cores: 4}}
	handler := New(fakeCollector{snapshot: want}, History{}, site)

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
	handler := New(fakeCollector{err: errors.New("no /proc")}, History{}, site)

	rec := get(handler, "/api/metrics", "127.0.0.1:5000")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestServesWebsite(t *testing.T) {
	handler := New(fakeCollector{}, History{}, site)

	rec := get(handler, "/", "10.0.0.5:5000")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestExplainsMissingWebsiteBuild(t *testing.T) {
	handler := New(fakeCollector{}, History{}, fstest.MapFS{})

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
	handler := New(fakeCollector{}, History{}, site)

	for _, tt := range tests {
		t.Run(tt.remoteAddr, func(t *testing.T) {
			rec := get(handler, "/api/metrics", tt.remoteAddr)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

type fakeHistory struct {
	from, to time.Time
	step     time.Duration
}

func (f *fakeHistory) Range(_ context.Context, _ string, from, to time.Time, step time.Duration) ([]history.Series, error) {
	f.from, f.to, f.step = from, to, step
	return []history.Series{{Metric: history.MetricCPU, Points: []history.Point{{Time: from.Unix(), Value: 12.5}}}}, nil
}

func TestHistoryReturnsTheRequestedRange(t *testing.T) {
	reader := &fakeHistory{}
	handler := New(fakeCollector{}, History{Reader: reader, Retention: 30 * 24 * time.Hour}, site)
	to := time.Now().Add(-time.Hour).Unix()
	from := to - 24*3600

	rec := get(handler, fmt.Sprintf("/api/history?from=%d&to=%d", from, to), "10.0.0.5:5000")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	var got historyResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.From != from || got.To != to || reader.from.Unix() != from || reader.to.Unix() != to {
		t.Errorf("range = %d to %d (read %v to %v), want %d to %d", got.From, got.To, reader.from, reader.to, from, to)
	}
	// One day at most 360 points: 4 minute steps.
	if got.StepSeconds != 240 || reader.step != 4*time.Minute {
		t.Errorf("StepSeconds = %d, step = %v, want 240", got.StepSeconds, reader.step)
	}
	if got.RetentionDays != 30 || len(got.Series) != 1 || got.Series[0].Points[0].Value != 12.5 {
		t.Errorf("response = %+v, want 30 retention days and the stored CPU usage", got)
	}
}

func TestHistoryLimitsTheRangeToTheRetention(t *testing.T) {
	reader := &fakeHistory{}
	handler := New(fakeCollector{}, History{Reader: reader, Retention: 24 * time.Hour}, site)

	rec := get(handler, "/api/history?from=0&to=9223372036854775807", "10.0.0.5:5000")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	now := time.Now()
	if reader.from.Before(now.Add(-25*time.Hour)) || reader.to.After(now.Add(time.Minute)) {
		t.Errorf("read %v to %v, want at most the last day", reader.from, reader.to)
	}
}

func TestHistoryRefusesInvalidRanges(t *testing.T) {
	handler := New(fakeCollector{}, History{Reader: &fakeHistory{}, Retention: time.Hour}, site)
	for _, query := range []string{"", "?from=1", "?from=a&to=b", "?from=200&to=100", "?from=100&to=100"} {
		if rec := get(handler, "/api/history"+query, "10.0.0.5:5000"); rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/history%s status = %d, want %d", query, rec.Code, http.StatusBadRequest)
		}
	}
}

func TestStepFor(t *testing.T) {
	tests := []struct {
		span time.Duration
		want time.Duration
	}{
		{time.Minute, time.Minute},
		{6 * time.Hour, time.Minute},
		{6*time.Hour + time.Minute, 2 * time.Minute},
		{30 * 24 * time.Hour, 2 * time.Hour},
	}
	for _, tt := range tests {
		if got := stepFor(tt.span); got != tt.want {
			t.Errorf("stepFor(%v) = %v, want %v", tt.span, got, tt.want)
		}
	}
}
