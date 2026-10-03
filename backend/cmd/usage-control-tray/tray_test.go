package main

import (
	"context"
	"net/http"
	"net/http/httptest"
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
