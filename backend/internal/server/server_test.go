package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/hub"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
	"github.com/Chriszly/usage-control/backend/internal/update"
)

// DeviceList is a fixed list of devices, as the tests need it.
type DeviceList []Device

func (l DeviceList) List() []Device {
	return l
}

type fakeCollector struct {
	snapshot metrics.Snapshot
	err      error
}

func (f fakeCollector) Collect(context.Context) (metrics.Snapshot, error) {
	return f.snapshot, f.err
}

// newHandler returns the site for a fixed list of devices, without adding
// and removing them.
func newHandler(devices []Device, retention time.Duration, files fs.FS) http.Handler {
	return New(Site{Devices: DeviceList(devices), Retention: retention, Files: files})
}

// device returns the machine the site runs on, as the only device.
func device(collector Collector, reader HistoryReader) []Device {
	return []Device{{ID: "local", Metrics: collector, History: reader}}
}

var site = fstest.MapFS{
	"index.html":       {Data: []byte("page")},
	"main.js":          {Data: []byte("script")},
	"main-7EIQR62F.js": {Data: []byte("hashed script")},
	"flags/de.svg":     {Data: []byte("flag")},
}

func get(handler http.Handler, path, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remoteAddr
	req.Host = "192.168.1.9:9393"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestMetricsReturnsSnapshotAsJSON(t *testing.T) {
	want := metrics.Snapshot{CPU: metrics.CPU{UsagePercent: 12.5, Cores: 4}}
	handler := newHandler(device(fakeCollector{snapshot: want}, nil), 0, site)

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
	if !reflect.DeepEqual(got.CPU, want.CPU) {
		t.Errorf("CPU = %+v, want %+v", got.CPU, want.CPU)
	}
}

func TestMetricsReportsCollectorError(t *testing.T) {
	handler := newHandler(device(fakeCollector{err: errors.New("no /proc")}, nil), 0, site)

	rec := get(handler, "/api/metrics", "127.0.0.1:5000")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestDataOnlyServesOnlyTheMetrics(t *testing.T) {
	want := metrics.Snapshot{CPU: metrics.CPU{UsagePercent: 12.5, Cores: 4}}
	handler := NewDataOnly(fakeCollector{snapshot: want}, nil)

	rec := get(handler, "/api/metrics", "192.168.1.20:5000")
	var got metrics.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET /api/metrics = %d, %v, want 200 with the snapshot", rec.Code, err)
	}
	if !reflect.DeepEqual(got.CPU, want.CPU) {
		t.Errorf("CPU = %+v, want %+v", got.CPU, want.CPU)
	}
	for _, path := range []string{"/", "/api/devices", "/api/history?range=1h", "/api/update"} {
		if rec := get(handler, path, "192.168.1.20:5000"); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}
	if rec := get(handler, "/api/metrics", "8.8.8.8:5000"); rec.Code != http.StatusForbidden {
		t.Errorf("GET /api/metrics from outside = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestListsDevices(t *testing.T) {
	devices := []Device{{ID: "local"}, {ID: "living-room-pi", Name: "Living room Pi"}}
	handler := newHandler(devices, 0, site)

	rec := get(handler, "/api/devices", "192.168.1.20:5000")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got devicesResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !slices.Equal(got.Devices, devices) || got.PasswordSet {
		t.Errorf("response = %+v, want devices %+v and no password", got, devices)
	}
}

func TestMetricsOfTheRequestedDevice(t *testing.T) {
	handler := newHandler([]Device{
		{ID: "local", Metrics: fakeCollector{snapshot: metrics.Snapshot{CPU: metrics.CPU{Cores: 4}}}},
		{ID: "pi", Metrics: fakeCollector{snapshot: metrics.Snapshot{CPU: metrics.CPU{Cores: 2}}}},
		{ID: "pc", Metrics: fakeCollector{err: fmt.Errorf("ask pc: %w", hub.ErrUnreachable)}},
	}, 0, site)
	tests := []struct {
		path       string
		wantStatus int
		wantCores  int
	}{
		{"/api/metrics", http.StatusOK, 4},
		{"/api/metrics?device=local", http.StatusOK, 4},
		{"/api/metrics?device=pi", http.StatusOK, 2},
		{"/api/metrics?device=pc", http.StatusServiceUnavailable, 0},
		{"/api/metrics?device=nas", http.StatusNotFound, 0},
		{"/api/history?device=nas&from=1&to=2", http.StatusNotFound, 0},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := get(handler, tt.path, "192.168.1.20:5000")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			var got metrics.Snapshot
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got.CPU.Cores != tt.wantCores {
				t.Errorf("cores = %d, want %d", got.CPU.Cores, tt.wantCores)
			}
		})
	}
}

func TestServesWebsite(t *testing.T) {
	handler := newHandler(device(fakeCollector{}, nil), 0, site)

	tests := []struct {
		path, wantBody, wantCache string
	}{
		{"/", "page", "no-cache"},
		{"/main-7EIQR62F.js", "hashed script", "public, max-age=31536000, immutable"},
		{"/main.js", "script", "public, max-age=3600"},
		{"/flags/de.svg", "flag", "public, max-age=3600"},
	}
	for _, tt := range tests {
		rec := get(handler, tt.path, "10.0.0.5:5000")

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want %d", tt.path, rec.Code, http.StatusOK)
		}
		if got := rec.Body.String(); got != tt.wantBody {
			t.Errorf("%s: body = %q, want %q", tt.path, got, tt.wantBody)
		}
		if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
			t.Errorf("%s: Cache-Control = %q, want %q", tt.path, got, tt.wantCache)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", tt.path, got)
		}
	}
}

func TestWebsiteServesNoDirectoriesOrMissingFiles(t *testing.T) {
	handler := newHandler(device(fakeCollector{}, nil), 0, site)

	for _, path := range []string{"/flags/", "/flags", "/missing-ABCDEFGH.js"} {
		rec := get(handler, path, "10.0.0.5:5000")

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
		if got := rec.Header().Get("Cache-Control"); got != "" {
			t.Errorf("%s: Cache-Control = %q, want none on a 404", path, got)
		}
	}
}

func TestAPIAnswersCarryNosniff(t *testing.T) {
	handler := newHandler(device(fakeCollector{}, nil), 0, site)

	rec := get(handler, "/api/metrics", "10.0.0.5:5000")

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestExplainsMissingWebsiteBuild(t *testing.T) {
	handler := newHandler(device(fakeCollector{}, nil), 0, fstest.MapFS{})

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
	handler := newHandler(device(fakeCollector{}, nil), 0, site)

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
	// newest is when the newest reading is from; zero when there is none.
	newest time.Time
	// series is what Range returns; nil returns one CPU value.
	series []history.Series
	extras map[string]history.ExtraInfo
}

func (f *fakeHistory) ExtraInfo(context.Context) (map[string]history.ExtraInfo, error) {
	return f.extras, nil
}

func (f *fakeHistory) Newest(context.Context) (time.Time, bool, error) {
	return f.newest, !f.newest.IsZero(), nil
}

func (f *fakeHistory) Range(_ context.Context, from, to time.Time) ([]history.Series, time.Duration, error) {
	f.from, f.to = from, to
	if f.series != nil {
		return f.series, time.Minute, nil
	}
	return []history.Series{{Metric: history.MetricCPU, Points: []history.Point{{Time: from.Unix(), Value: 12.5}}}}, 4 * time.Minute, nil
}

func TestHistoryReturnsTheRequestedRange(t *testing.T) {
	reader := &fakeHistory{}
	handler := newHandler(device(fakeCollector{}, reader), 30*24*time.Hour, site)
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
	if got.StepSeconds != 240 {
		t.Errorf("StepSeconds = %d, want the reader's step of 240", got.StepSeconds)
	}
	if got.RetentionDays != 30 || len(got.Series) != 1 || got.Series[0].Points[0].Value != 12.5 {
		t.Errorf("response = %+v, want 30 retention days and the stored CPU usage", got)
	}
}

func TestHistoryLimitsTheRangeToTheRetention(t *testing.T) {
	reader := &fakeHistory{}
	handler := newHandler(device(fakeCollector{}, reader), 24*time.Hour, site)

	rec := get(handler, "/api/history?from=0&to=9223372036854775807", "10.0.0.5:5000")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	now := time.Now()
	if reader.from.Before(now.Add(-25*time.Hour)) || reader.to.After(now.Add(time.Minute)) {
		t.Errorf("read %v to %v, want at most the last day", reader.from, reader.to)
	}
}

func TestHistoryOfADeviceNotAnsweringEndsAtItsNewestReading(t *testing.T) {
	newest := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	reader := &fakeHistory{newest: newest}
	handler := newHandler([]Device{{ID: "local"}, {ID: "laptop", Unreachable: true, History: reader}}, 30*24*time.Hour, site)
	to := time.Now().Unix()

	rec := get(handler, fmt.Sprintf("/api/history?device=laptop&from=%d&to=%d", to-60, to), "10.0.0.5:5000")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	var got historyResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	wantTo := newest.Unix() + 1
	if got.From != wantTo-60 || got.To != wantTo || reader.to.Unix() != wantTo {
		t.Errorf("range = %d to %d, want the minute up to the newest reading, %d to %d", got.From, got.To, wantTo-60, wantTo)
	}
	if got.LastReading != newest.Unix() {
		t.Errorf("LastReading = %d, want %d", got.LastReading, newest.Unix())
	}
}

func TestHistoryOfADeviceNotAnsweringWithoutReadingsStaysAsAsked(t *testing.T) {
	reader := &fakeHistory{}
	handler := newHandler([]Device{{ID: "local"}, {ID: "laptop", Unreachable: true, History: reader}}, 24*time.Hour, site)
	to := time.Now().Unix()

	rec := get(handler, fmt.Sprintf("/api/history?device=laptop&from=%d&to=%d", to-60, to), "10.0.0.5:5000")

	var got historyResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.To != to || got.LastReading != 0 {
		t.Errorf("response = %+v, want the range as asked and no last reading", got)
	}
}

func TestHistoryOfAnAnsweringDeviceIgnoresItsNewestReading(t *testing.T) {
	reader := &fakeHistory{newest: time.Now().Add(-time.Hour)}
	handler := newHandler(device(fakeCollector{}, reader), 24*time.Hour, site)
	to := time.Now().Unix()

	rec := get(handler, fmt.Sprintf("/api/history?from=%d&to=%d", to-60, to), "10.0.0.5:5000")

	var got historyResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.To != to || got.LastReading != 0 {
		t.Errorf("response = %+v, want the range as asked and no last reading", got)
	}
}

func TestHistoryRefusesInvalidRanges(t *testing.T) {
	handler := newHandler(device(fakeCollector{}, &fakeHistory{}), time.Hour, site)
	for _, query := range []string{"", "?from=1", "?from=a&to=b", "?from=200&to=100", "?from=100&to=100"} {
		if rec := get(handler, "/api/history"+query, "10.0.0.5:5000"); rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/history%s status = %d, want %d", query, rec.Code, http.StatusBadRequest)
		}
	}
}

type fakeAvailability hub.Availability

func (f fakeAvailability) Availability(context.Context) (hub.Availability, error) {
	return hub.Availability(f), nil
}

func TestAvailabilityOfAnotherDevice(t *testing.T) {
	since := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	handler := newHandler([]Device{
		{ID: "local"},
		{ID: "pi", Availability: fakeAvailability{Since: since, OfflineSeconds: 90, Outages: 1}},
	}, 0, site)

	if rec := get(handler, "/api/availability", "192.168.1.20:5000"); rec.Code != http.StatusNotFound {
		t.Errorf("status for this device = %d, want %d", rec.Code, http.StatusNotFound)
	}
	rec := get(handler, "/api/availability?device=pi", "192.168.1.20:5000")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got hub.Availability
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Since.Equal(since) || got.OfflineSeconds != 90 || got.Outages != 1 || got.LastOutage != nil {
		t.Errorf("availability = %+v", got)
	}
}

func TestUpdateTellsANewerRelease(t *testing.T) {
	want := update.Status{Current: "0.1.0", Latest: "0.2.0", URL: "https://github.com/Chriszly/usage-control/releases/tag/v0.2.0"}
	handler := New(Site{Devices: DeviceList(device(fakeCollector{}, nil)), Files: site, Update: func() update.Status { return want }})

	rec := get(handler, "/api/update", "192.168.1.20:5000")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got update.Status
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got != want {
		t.Errorf("update = %+v, want %+v", got, want)
	}
}

func TestHistoryDescribesTheExtras(t *testing.T) {
	points := []history.Point{{Time: time.Now().Unix(), Value: 1}}
	described := history.ExtraInfo{Title: "Pressure", Label: "CPU", Unit: metrics.UnitPercent}
	reader := &fakeHistory{
		series: []history.Series{
			{Metric: history.MetricCPU, Points: points},
			{Metric: "extra:pressure/cpu", Points: points},
			{Metric: "extra:pressure/unknown", Points: points},
		},
		extras: map[string]history.ExtraInfo{"extra:pressure/cpu": described, "extra:other/x": {}},
	}
	handler := newHandler(device(fakeCollector{}, reader), 30*24*time.Hour, site)
	to := time.Now().Unix()

	rec := get(handler, fmt.Sprintf("/api/history?from=%d&to=%d", to-3600, to), "10.0.0.5:5000")

	var got historyResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	metricNames := []string{}
	for _, s := range got.Series {
		metricNames = append(metricNames, s.Metric)
	}
	if !reflect.DeepEqual(metricNames, []string{history.MetricCPU, "extra:pressure/cpu"}) {
		t.Errorf("series = %v, want the CPU and the described extra", metricNames)
	}
	if want := map[string]history.ExtraInfo{"extra:pressure/cpu": described}; !reflect.DeepEqual(got.Extras, want) {
		t.Errorf("extras = %+v, want %+v", got.Extras, want)
	}
}
