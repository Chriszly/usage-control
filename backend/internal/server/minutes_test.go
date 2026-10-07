package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/hub"
)

type fakeMinutes struct {
	minutes []history.Minute
	after   time.Time
	limit   int
}

func (f *fakeMinutes) Since(_ context.Context, after time.Time, limit int) ([]history.Minute, bool, error) {
	f.after, f.limit = after, limit
	return f.minutes, true, nil
}

func TestServesTheMinutesForAHub(t *testing.T) {
	source := &fakeMinutes{minutes: []history.Minute{{Time: 1_700_000_060, Values: map[string]float64{"cpu": 12}}}}
	handlers := map[string]http.Handler{
		"data only": NewDataOnly(fakeCollector{}, source, nil),
		"website":   New(Site{Devices: DeviceList(device(fakeCollector{}, nil)), Files: site, Minutes: source}),
	}
	for name, handler := range handlers {
		rec := get(handler, "/api/minutes?after=1700000000", "192.168.1.20:5000")
		var got hub.MinutesAnswer
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s: GET /api/minutes = %d, %v, want 200", name, rec.Code, err)
		}
		if !reflect.DeepEqual(got.Minutes, source.minutes) || !got.More || time.Since(time.Unix(got.Now, 0)) > time.Minute {
			t.Errorf("%s: answer = %+v, want the minutes, more and the time now", name, got)
		}
		if source.after.Unix() != 1_700_000_000 || source.limit != hub.MinutesPerAnswer {
			t.Errorf("%s: asked the source after %v for %d, want after 1700000000 for %d", name, source.after.Unix(), source.limit, hub.MinutesPerAnswer)
		}
		// A time later than the device's would tell that the hub has minutes
		// the device has not even measured yet.
		later := "/api/minutes?after=" + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
		for _, path := range []string{"/api/minutes", "/api/minutes?after=-1", "/api/minutes?after=yesterday", later} {
			source.after = time.Time{}
			if rec := get(handler, path, "192.168.1.20:5000"); rec.Code != http.StatusBadRequest || !source.after.IsZero() {
				t.Errorf("%s: GET %s = %d and asked the source after %v, want %d without asking", name, path, rec.Code, source.after, http.StatusBadRequest)
			}
		}
	}
}

func TestDataOnlyWithoutMinutesLeavesThemOut(t *testing.T) {
	handler := NewDataOnly(fakeCollector{}, nil, nil)
	// A hub takes the device for one too old to keep minutes, and stores the
	// average of its own readings instead.
	if rec := get(handler, "/api/minutes?after=0", "192.168.1.20:5000"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/minutes without minutes = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if rec := get(handler, "/api/metrics", "192.168.1.20:5000"); rec.Code != http.StatusOK {
		t.Errorf("GET /api/metrics without minutes = %d, want %d", rec.Code, http.StatusOK)
	}
}
