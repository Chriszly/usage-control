package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
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
	source := &fakeMinutes{minutes: []history.Minute{{Time: 1_800_000_060, Values: map[string]float64{"cpu": 12}}}}
	handlers := map[string]http.Handler{
		"data only": NewDataOnly(fakeCollector{}, source, nil),
		"website":   New(Site{Devices: DeviceList(device(fakeCollector{}, nil)), Files: site, Minutes: source}),
	}
	for name, handler := range handlers {
		rec := get(handler, "/api/minutes?after=1800000000", "192.168.1.20:5000")
		var got hub.MinutesAnswer
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s: GET /api/minutes = %d, %v, want 200", name, rec.Code, err)
		}
		if !reflect.DeepEqual(got.Minutes, source.minutes) || !got.More || time.Since(time.Unix(got.Now, 0)) > time.Minute {
			t.Errorf("%s: answer = %+v, want the minutes, more and the time now", name, got)
		}
		if source.after.Unix() != 1_800_000_000 || source.limit != hub.MinutesPerAnswer {
			t.Errorf("%s: asked the source after %v for %d, want after 1800000000 for %d", name, source.after.Unix(), source.limit, hub.MinutesPerAnswer)
		}
		for _, path := range []string{"/api/minutes", "/api/minutes?after=-1", "/api/minutes?after=yesterday"} {
			if rec := get(handler, path, "192.168.1.20:5000"); rec.Code != http.StatusBadRequest {
				t.Errorf("%s: GET %s = %d, want %d", name, path, rec.Code, http.StatusBadRequest)
			}
		}
	}
}
