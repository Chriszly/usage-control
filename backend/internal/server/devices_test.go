package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chriszly/usage-control/backend/internal/hub"
	"github.com/Chriszly/usage-control/backend/internal/password"
)

type fakeHub struct {
	added   []string
	removed []string
	err     error
}

func (f *fakeHub) Add(_ context.Context, name, address string) (hub.Device, error) {
	if f.err != nil {
		return hub.Device{}, f.err
	}
	f.added = append(f.added, name)
	return hub.Device{ID: strings.ToLower(name), Name: name, Address: address}, nil
}

func (f *fakeHub) Remove(_ context.Context, id string, _ bool) error {
	if f.err != nil {
		return f.err
	}
	f.removed = append(f.removed, id)
	return nil
}

type fakePassword struct{ password string }

func (f *fakePassword) IsSet(context.Context) (bool, error) { return f.password != "", nil }

func (f *fakePassword) Check(_ context.Context, given string) error {
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
	if body := rec.Body.String(); !strings.Contains(body, `"removable":true`) {
		t.Errorf("body = %s, want the added device", body)
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
