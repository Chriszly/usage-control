package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/hub"
)

type fakeMinutes struct {
	minutes []history.Minute
	extras  map[string]history.ExtraInfo
	hub     string
	after   time.Time
	limit   int
	values  int
}

func (f *fakeMinutes) Since(_ context.Context, hub string, after time.Time, minutes, values int) ([]history.Minute, bool, error) {
	f.hub, f.after, f.limit, f.values = hub, after, minutes, values
	return f.minutes, true, nil
}

func (f *fakeMinutes) ExtraInfo(context.Context) (map[string]history.ExtraInfo, error) {
	return f.extras, nil
}

func TestServesTheMinutesForAHub(t *testing.T) {
	source := &fakeMinutes{minutes: []history.Minute{{Time: 1_700_000_060, Values: map[string]float64{"cpu": 12}}}}
	handlers := map[string]http.Handler{
		"data only": NewDataOnly(fakeCollector{}, source, nil),
		"website":   New(Site{Devices: DeviceList(device(fakeCollector{}, nil)), Files: site, Minutes: source}),
	}
	for name, handler := range handlers {
		rec := getFromHub(handler, "/api/minutes?after=1700000000", "192.168.1.20:5000")
		var got hub.MinutesAnswer
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s: GET /api/minutes = %d, %v, want 200", name, rec.Code, err)
		}
		if !reflect.DeepEqual(got.Minutes, source.minutes) || !got.More || time.Since(time.Unix(got.Now, 0)) > time.Minute {
			t.Errorf("%s: answer = %+v, want the minutes, more and the time now", name, got)
		}
		if source.after.Unix() != 1_700_000_000 || source.limit != hub.MinutesPerAnswer || source.values != hub.ValuesPerAnswer || source.hub != testHubID {
			t.Errorf("%s: asked the source after %v for %d minutes and %d values for %q, want after 1700000000 for %d and %d for %s",
				name, source.after.Unix(), source.limit, source.values, source.hub, hub.MinutesPerAnswer, hub.ValuesPerAnswer, testHubID)
		}
		// A hub may ask for fewer values, not for more.
		for asked, want := range map[string]int{"500": 500, "99999999": hub.ValuesPerAnswer} {
			if rec := get(handler, "/api/minutes?after=1700000000&values="+asked, "192.168.1.20:5000"); rec.Code != http.StatusOK || source.values != want {
				t.Errorf("%s: GET with values=%s = %d, asked the source for %d values; want 200 and %d", name, asked, rec.Code, source.values, want)
			}
		}
		// A time later than the device's would tell that the hub has minutes
		// the device has not even measured yet.
		later := "/api/minutes?after=" + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
		for _, path := range []string{"/api/minutes", "/api/minutes?after=-1", "/api/minutes?after=yesterday", later, "/api/minutes?after=1700000000&values=0", "/api/minutes?after=1700000000&values=many"} {
			source.after = time.Time{}
			if rec := get(handler, path, "192.168.1.20:5000"); rec.Code != http.StatusBadRequest || !source.after.IsZero() {
				t.Errorf("%s: GET %s = %d and asked the source after %v, want %d without asking", name, path, rec.Code, source.after, http.StatusBadRequest)
			}
		}
	}
}

// testHubID is the id getFromHub sends.
const testHubID = "0123456789abcdef0123456789abcdef"

// getFromHub is get, as a hub asks, with hub.PagePortHeader and
// hub.HubIDHeader.
func getFromHub(handler http.Handler, path, remoteAddr string) *httptest.ResponseRecorder {
	return getFromHubWithID(handler, path, remoteAddr, testHubID)
}

func getFromHubWithID(handler http.Handler, path, remoteAddr, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remoteAddr
	req.Host = "192.168.1.9:9393"
	req.Header.Set(hub.PagePortHeader, "9393")
	if id != "" {
		req.Header.Set(hub.HubIDHeader, id)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestTellsAHubApartByItsID(t *testing.T) {
	source := &fakeMinutes{minutes: []history.Minute{}}
	handler := NewDataOnly(fakeCollector{}, source, nil)
	// Two hubs behind one address are two hubs; one hub whose address changes
	// is one.
	for _, c := range []struct{ remote, id string }{
		{"192.168.1.20:5000", testHubID},
		{"192.168.1.20:5001", "fedcba9876543210fedcba9876543210"},
		{"[fd00::20]:5003", testHubID},
	} {
		if rec := getFromHubWithID(handler, "/api/minutes?after=0", c.remote, c.id); rec.Code != http.StatusOK || source.hub != c.id {
			t.Errorf("GET from %s with id %s = %d, asked the source for %q; want 200 and the id", c.remote, c.id, rec.Code, source.hub)
		}
	}
	// A request without the headers, or without a valid id, as from a hub
	// that sends none, is not from a hub: it deletes nothing and adds no hub.
	for _, id := range []string{"", "not an id", strings.Repeat("a", 33), strings.ToUpper(testHubID)} {
		if rec := getFromHubWithID(handler, "/api/minutes?after=0", "192.168.1.20:5000", id); rec.Code != http.StatusOK || source.hub != "" {
			t.Errorf("GET with id %q = %d, asked the source for %q; want 200 and no hub", id, rec.Code, source.hub)
		}
	}
	if rec := get(handler, "/api/minutes?after=0", "192.168.1.20:5000"); rec.Code != http.StatusOK || source.hub != "" {
		t.Errorf("GET without the hub's headers = %d, asked the source for %q; want 200 and no hub", rec.Code, source.hub)
	}
}

func TestServesHowTheExtrasAmongTheMinutesAreDescribed(t *testing.T) {
	power := history.ExtraInfo{Title: "Power", Label: "CPU", Unit: "watts"}
	source := &fakeMinutes{
		minutes: []history.Minute{{Time: 1_700_000_060, Values: map[string]float64{"cpu": 12, "extra:power/cpu": 5}}},
		extras:  map[string]history.ExtraInfo{"extra:power/cpu": power, "extra:power/gpu": {Title: "Power", Label: "GPU"}},
	}
	rec := get(NewDataOnly(fakeCollector{}, source, nil), "/api/minutes?after=1700000000", "192.168.1.20:5000")
	var got hub.MinutesAnswer
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET /api/minutes = %d, %v, want 200", rec.Code, err)
	}
	if want := map[string]history.ExtraInfo{"extra:power/cpu": power}; !reflect.DeepEqual(got.Extras, want) {
		t.Errorf("extras = %+v, want %+v, only the ones among the minutes", got.Extras, want)
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
