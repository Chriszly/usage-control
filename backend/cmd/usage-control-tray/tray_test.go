package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"testing"
)

func TestStateOf(t *testing.T) {
	tests := []struct {
		service        serviceState
		answers, pause bool
		want           state
		icon           string
	}{
		{serviceRunning, true, false, stateRunning, "running.ico"},
		{serviceRunning, false, false, stateNotAnswering, "stopped.ico"},
		{serviceStarting, false, false, stateStarting, "paused.ico"},
		{serviceStopping, false, true, stateStopping, "paused.ico"},
		{serviceStopped, false, false, stateStopped, "stopped.ico"},
		{serviceStopped, false, true, statePaused, "paused.ico"},
		{serviceMissing, false, false, stateMissing, "stopped.ico"},
	}
	for _, tt := range tests {
		got := stateOf(tt.service, tt.answers, tt.pause)
		if got != tt.want || got.icon() != tt.icon || got.running() != (tt.service == serviceRunning || tt.service == serviceStarting) {
			t.Errorf("stateOf(%d, %v, %v) = %d with %s, want %d with %s", tt.service, tt.answers, tt.pause, got, got.icon(), tt.want, tt.icon)
		}
	}
}

func TestStillPaused(t *testing.T) {
	for service, want := range map[serviceState]bool{
		serviceStopping: true,
		serviceStopped:  true,
		serviceMissing:  true,
		serviceStarting: false,
		serviceRunning:  false,
	} {
		if got := stillPaused(service, true); got != want {
			t.Errorf("stillPaused(%d, true) = %v, want %v", service, got, want)
		}
		if stillPaused(service, false) {
			t.Errorf("stillPaused(%d, false) = true, want false", service)
		}
	}
}

func TestNewSettings(t *testing.T) {
	tests := []struct {
		port, website string
		want          settings
	}{
		{"", "", settings{port: "9393"}},
		{"8091", "1", settings{port: "8091", website: true}},
		{"0", "0", settings{port: "9393"}},
		{"70000", "yes", settings{port: "9393"}},
		{"80/x", "1", settings{port: "9393", website: true}},
	}
	for _, tt := range tests {
		if got := newSettings(tt.port, tt.website); got != tt.want {
			t.Errorf("newSettings(%q, %q) = %+v, want %+v", tt.port, tt.website, got, tt.want)
		}
	}
	if got, want := newSettings("8091", "1").pageURL(), "http://localhost:8091/"; got != want {
		t.Errorf("pageURL() = %q, want %q", got, want)
	}
}

// serveHub answers GET /api/hub with body and returns the port it listens on.
func serveHub(t *testing.T, status int, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/hub" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	address, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return address.Port()
}

func TestAskHub(t *testing.T) {
	port := serveHub(t, http.StatusOK, `{"url":"http://192.168.1.20:9393/"}`)
	link, err := askHub(context.Background(), http.DefaultClient, port)
	if err != nil || link != "http://192.168.1.20:9393/" {
		t.Errorf("askHub() = %q, %v, want the hub's page", link, err)
	}

	port = serveHub(t, http.StatusOK, `{"url":""}`)
	if link, err := askHub(context.Background(), http.DefaultClient, port); err != nil || link != "" {
		t.Errorf("askHub() before a hub asked = %q, %v, want no link and no error", link, err)
	}
}

func TestAskHubRefusesOtherAnswers(t *testing.T) {
	for _, answer := range []struct {
		status int
		body   string
	}{
		{http.StatusForbidden, `{"url":""}`},
		{http.StatusOK, `not json`},
		{http.StatusOK, `{"url":"file:///C:/Windows/System32/calc.exe"}`},
		{http.StatusOK, `{"url":"http://user@192.168.1.20/"}`},
		{http.StatusOK, `{"url":"http:///"}`},
	} {
		port := serveHub(t, answer.status, answer.body)
		if link, err := askHub(context.Background(), http.DefaultClient, port); err == nil {
			t.Errorf("askHub() with %d %s = %q, want an error", answer.status, answer.body, link)
		}
	}
}

func TestTextsFor(t *testing.T) {
	if got := textsFor([]string{"de-DE", "en-US"}).pause; got != "Pausieren" {
		t.Errorf("de-DE: pause = %q, want Pausieren", got)
	}
	if got := textsFor([]string{"it-IT", "fr-CA"}).pause; got != "Mettre en pause" {
		t.Errorf("it-IT, fr-CA: pause = %q, want the French one", got)
	}
	if got := textsFor(nil).pause; got != "Pause" {
		t.Errorf("no languages: pause = %q, want English", got)
	}
}

func TestEveryLanguageHasEveryText(t *testing.T) {
	for language, text := range translations {
		value := reflect.ValueOf(text)
		for i := range value.NumField() {
			if value.Field(i).String() == "" {
				t.Errorf("%s: %s is empty", language, value.Type().Field(i).Name)
			}
		}
		for s := stateRunning; s <= stateMissing; s++ {
			if text.stateText(s) == "" {
				t.Errorf("%s: state %d has no text", language, s)
			}
		}
	}
}

func TestPickAddress(t *testing.T) {
	addr := netip.MustParseAddr
	tests := []struct {
		name       string
		routed     netip.Addr
		candidates []netip.Addr
		want       string
	}{
		{"the routed one first", addr("192.168.60.20"), []netip.Addr{addr("10.0.75.1"), addr("192.168.60.20")}, "192.168.60.20"},
		{"no route", netip.Addr{}, []netip.Addr{addr("fe80::1"), addr("169.254.3.4"), addr("172.16.0.5")}, "172.16.0.5"},
		{"public route", addr("203.0.113.7"), []netip.Addr{addr("203.0.113.7"), addr("10.1.2.3")}, "10.1.2.3"},
		{"IPv4 in IPv6", addr("::ffff:192.168.1.9"), nil, "192.168.1.9"},
		{"none", addr("203.0.113.7"), []netip.Addr{addr("fd00::1"), addr("127.0.0.1")}, ""},
	}
	for _, tt := range tests {
		got, found := pickAddress(tt.routed, tt.candidates)
		if (tt.want == "" && found) || (tt.want != "" && (!found || got.String() != tt.want)) {
			t.Errorf("%s: pickAddress() = %v, %v, want %q", tt.name, got, found, tt.want)
		}
	}
}

func TestAddressText(t *testing.T) {
	text := translations["en"]
	if got, want := text.addressText(netip.MustParseAddr("192.168.60.20"), true, "9393"), "Address: 192.168.60.20:9393"; got != want {
		t.Errorf("addressText() = %q, want %q", got, want)
	}
	if got, want := text.addressText(netip.Addr{}, false, "9393"), "Address: no local network"; got != want {
		t.Errorf("addressText() without an address = %q, want %q", got, want)
	}
}

func TestLocalAddressIsAPrivateIPv4Address(t *testing.T) {
	if addr, found := localAddress(); found && (!addr.Is4() || !addr.IsPrivate()) {
		t.Errorf("localAddress() = %v, want a private IPv4 address or none", addr)
	}
}
