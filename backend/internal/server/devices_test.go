package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/Chriszly/usage-control/backend/internal/hub"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
	"github.com/Chriszly/usage-control/backend/internal/password"
)

type fakeHub struct {
	added   []string
	removed []string
	// kinds is every kind added or set, as "id=kind".
	kinds []string
	err   error
	// suggestFor answers Suggest for this address; any other has no suggestion.
	suggestFor string
	asked      []netip.Addr
	// own is the machine's own addresses the last Suggest got.
	own []netip.Addr
}

func (f *fakeHub) Add(_ context.Context, name, address string, kind hub.Kind) (hub.Device, error) {
	if f.err != nil {
		return hub.Device{}, f.err
	}
	f.added = append(f.added, name)
	f.kinds = append(f.kinds, strings.ToLower(name)+"="+string(kind))
	return hub.Device{ID: strings.ToLower(name), Name: name, Address: address}, nil
}

func (f *fakeHub) Remove(_ context.Context, id string, _ bool) error {
	if f.err != nil {
		return f.err
	}
	f.removed = append(f.removed, id)
	return nil
}

func (f *fakeHub) SetKind(_ context.Context, id string, kind hub.Kind) error {
	if f.err != nil {
		return f.err
	}
	f.kinds = append(f.kinds, id+"="+string(kind))
	return nil
}

func (f *fakeHub) Suggest(_ context.Context, from netip.Addr, own []netip.Addr) (hub.Suggestion, bool) {
	f.asked, f.own = append(f.asked, from), own
	if from.String() != f.suggestFor || slices.Contains(own, from) {
		return hub.Suggestion{}, false
	}
	return hub.Suggestion{Address: from.String() + ":9393", Name: "Office PC", Kind: hub.KindPC}, true
}

type fakePassword struct{ password string }

func (f *fakePassword) IsSet(context.Context) (bool, error) { return f.password != "", nil }

func (f *fakePassword) Check(_ context.Context, _ netip.Addr, given string) error {
	switch {
	case f.password == "":
		return password.ErrNotSet
	case given != f.password:
		return password.ErrWrong
	}
	return nil
}

func (f *fakePassword) Set(_ context.Context, given string) error {
	if f.password != "" {
		return password.ErrAlreadySet
	}
	f.password = given
	return nil
}

func send(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.168.1.20:5000"
	req.Host = "192.168.1.9:9393"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func newChangeHandler(h Hub, p Password) http.Handler {
	return New(Site{Devices: DeviceList(device(fakeCollector{}, nil)), Hub: h, Password: p, Files: site})
}

func TestTheFirstAddedDeviceChoosesThePassword(t *testing.T) {
	devices := &fakeHub{}
	pw := &fakePassword{}
	handler := newChangeHandler(devices, pw)

	rec := send(handler, http.MethodPost, "/api/devices", `{"name":"Office PC","address":"192.168.1.30:9393","password":"correct horse"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body)
	}
	if pw.password != "correct horse" || len(devices.added) != 1 {
		t.Errorf("password = %q, added = %q; want the password chosen and the device added", pw.password, devices.added)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"kind":"server","removable":true`) {
		t.Errorf("body = %s, want the added device, a server", body)
	}
}

func TestAddAndChangeTheKind(t *testing.T) {
	devices := &fakeHub{}
	handler := newChangeHandler(devices, &fakePassword{password: "correct horse"})

	add := send(handler, http.MethodPost, "/api/devices", `{"name":"Laptop","address":"192.168.1.30:9393","kind":"pc","password":"correct horse"}`)
	set := send(handler, http.MethodPut, "/api/devices/pi/kind", `{"kind":"pc","password":"correct horse"}`)
	wrong := send(handler, http.MethodPut, "/api/devices/pi/kind", `{"kind":"server","password":"wrong"}`)
	unknown := send(handler, http.MethodPut, "/api/devices/pi/kind", `{"kind":"phone","password":"correct horse"}`)

	if add.Code != http.StatusCreated || set.Code != http.StatusNoContent {
		t.Errorf("add = %d, set = %d; want 201 and 204", add.Code, set.Code)
	}
	if wrong.Code != http.StatusForbidden {
		t.Errorf("with a wrong password: status = %d, want 403", wrong.Code)
	}
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), `"problem":"kind"`) {
		t.Errorf("an unknown kind: status = %d, body = %s; want 400 kind", unknown.Code, unknown.Body)
	}
	if got := strings.Join(devices.kinds, " "); got != "laptop=pc pi=pc" {
		t.Errorf("kinds = %s, want laptop=pc pi=pc", got)
	}
}

func TestChangesNeedThePassword(t *testing.T) {
	devices := &fakeHub{}
	handler := newChangeHandler(devices, &fakePassword{password: "correct horse"})

	add := send(handler, http.MethodPost, "/api/devices", `{"name":"Office PC","address":"192.168.1.30:9393","password":"wrong"}`)
	remove := send(handler, http.MethodDelete, "/api/devices/office-pc", `{"password":"wrong"}`)

	for _, rec := range []*httptest.ResponseRecorder{add, remove} {
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"problem":"wrongPassword"`) {
			t.Errorf("status = %d, body = %s; want 403 wrongPassword", rec.Code, rec.Body)
		}
	}
	if len(devices.added) != 0 || len(devices.removed) != 0 {
		t.Errorf("added = %q, removed = %q; want no change", devices.added, devices.removed)
	}

	rec := send(handler, http.MethodDelete, "/api/devices/office-pc", `{"password":"correct horse"}`)
	if rec.Code != http.StatusNoContent || len(devices.removed) != 1 {
		t.Errorf("remove with the password: status = %d, removed = %q; want 204 and the device removed", rec.Code, devices.removed)
	}
}

// slowPassword holds every wrong guess until release is closed.
type slowPassword struct {
	fakePassword
	guessing chan struct{}
	release  chan struct{}
}

func (s *slowPassword) Check(ctx context.Context, from netip.Addr, given string) error {
	if given != s.password {
		s.guessing <- struct{}{}
		<-s.release
	}
	return s.fakePassword.Check(ctx, from, given)
}

func TestAWrongGuessDoesNotHoldUpOtherChanges(t *testing.T) {
	devices := &fakeHub{}
	pw := &slowPassword{fakePassword: fakePassword{password: "correct horse"}, guessing: make(chan struct{}), release: make(chan struct{})}
	handler := newChangeHandler(devices, pw)

	guessed := make(chan int)
	go func() {
		guessed <- send(handler, http.MethodDelete, "/api/devices/nas", `{"password":"wrong"}`).Code
	}()
	<-pw.guessing

	if rec := send(handler, http.MethodDelete, "/api/devices/office-pc", `{"password":"correct horse"}`); rec.Code != http.StatusNoContent {
		t.Errorf("status while a wrong guess is checked = %d, want %d", rec.Code, http.StatusNoContent)
	}
	close(pw.release)
	if code := <-guessed; code != http.StatusForbidden {
		t.Errorf("status of the wrong guess = %d, want %d", code, http.StatusForbidden)
	}
}

func TestAFailedFirstChangeChoosesNoPassword(t *testing.T) {
	pw := &fakePassword{}
	handler := newChangeHandler(&fakeHub{err: &hub.InputError{Problem: hub.ProblemUnreachable, Message: "no answer"}}, pw)

	rec := send(handler, http.MethodPost, "/api/devices", `{"name":"Office PC","address":"192.168.1.30:9393","password":"correct horse"}`)

	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"problem":"unreachable"`) {
		t.Errorf("status = %d, body = %s; want 422 unreachable", rec.Code, rec.Body)
	}
	if pw.password != "" {
		t.Errorf("password = %q, want none chosen", pw.password)
	}
}

func TestRefusedChangesAnswerWithTheirStatus(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{&hub.InputError{Problem: hub.ProblemNameTaken}, http.StatusConflict},
		{&hub.InputError{Problem: hub.ProblemAddressTaken}, http.StatusConflict},
		{&hub.InputError{Problem: hub.ProblemFixed}, http.StatusConflict},
		{&hub.InputError{Problem: hub.ProblemRemoving}, http.StatusConflict},
		{&hub.InputError{Problem: hub.ProblemNotFound}, http.StatusNotFound},
		{&hub.InputError{Problem: hub.ProblemName}, http.StatusBadRequest},
		{hub.ErrStopping, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		handler := newChangeHandler(&fakeHub{err: tt.err}, &fakePassword{password: "correct horse"})
		rec := send(handler, http.MethodPost, "/api/devices", `{"name":"Office PC","address":"192.168.1.30:9393","password":"correct horse"}`)
		if rec.Code != tt.want {
			t.Errorf("%#v: status = %d, want %d", tt.err, rec.Code, tt.want)
		}
	}
}

func TestRefusesAShortFirstPassword(t *testing.T) {
	devices := &fakeHub{}
	handler := newChangeHandler(devices, &fakePassword{})

	rec := send(handler, http.MethodPost, "/api/devices", `{"name":"Office PC","address":"192.168.1.30:9393","password":"short"}`)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"problem":"passwordLength"`) {
		t.Errorf("status = %d, body = %s; want 400 passwordLength", rec.Code, rec.Body)
	}
	if len(devices.added) != 0 {
		t.Errorf("added = %q, want nothing", devices.added)
	}
}

func TestChangesOnlyAcceptJSON(t *testing.T) {
	handler := newChangeHandler(&fakeHub{}, &fakePassword{})
	req := httptest.NewRequest(http.MethodPost, "/api/devices", strings.NewReader("name=x&address=y&password=z"))
	req.RemoteAddr = "192.168.1.20:5000"
	req.Host = "192.168.1.9:9393"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnsupportedMediaType)
	}
}

func TestSuggestsTheVisitorsDevice(t *testing.T) {
	devices := &fakeHub{suggestFor: "192.168.1.20"}
	handler := newChangeHandler(devices, &fakePassword{})

	rec := send(handler, http.MethodGet, "/api/devices/suggestion", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if got, want := strings.TrimSpace(rec.Body.String()), `{"address":"192.168.1.20:9393","name":"Office PC","kind":"pc"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if len(devices.asked) != 1 || devices.asked[0] != netip.MustParseAddr("192.168.1.20") {
		t.Errorf("asked for %v, want the address the request came from", devices.asked)
	}

	devices.suggestFor = ""
	if rec := send(handler, http.MethodGet, "/api/devices/suggestion", ""); rec.Code != http.StatusNoContent {
		t.Errorf("without a suggestion: status = %d, want 204", rec.Code)
	}
}

func TestDoesNotSuggestTheHubItself(t *testing.T) {
	devices := &fakeHub{suggestFor: "192.168.1.20"}
	// In Docker, the hub's own addresses are not the container's; its usage
	// lists them.
	own := fakeCollector{snapshot: metrics.Snapshot{Network: []metrics.NetworkInterface{
		{Name: "eth0", Addresses: []string{"192.168.1.20"}},
	}}}
	handler := New(Site{Devices: DeviceList(device(own, nil)), Hub: devices, Password: &fakePassword{}, Files: site})

	if rec := send(handler, http.MethodGet, "/api/devices/suggestion", ""); rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if !slices.Contains(devices.own, netip.MustParseAddr("192.168.1.20")) {
		t.Errorf("own addresses given to the hub = %v, want the ones from the usage", devices.own)
	}
}

func TestNewWithoutTheHubsOwnDeviceDoesNotChangeDevices(t *testing.T) {
	handler := New(Site{Devices: DeviceList(nil), Hub: &fakeHub{}, Password: &fakePassword{}, Files: site})
	if rec := send(handler, http.MethodGet, "/api/devices/suggestion", ""); rec.Code == http.StatusNoContent || rec.Code == http.StatusOK {
		t.Errorf("status = %d, want the suggestion refused without the hub's own device", rec.Code)
	}
}
