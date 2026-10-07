package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Chriszly/usage-control/backend/internal/hub"
)

// askAsHub asks for the metrics like a hub does, from remoteAddr, telling
// the port of its page.
func askAsHub(handler http.Handler, remoteAddr, port string) {
	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	req.RemoteAddr = remoteAddr
	req.Host = "192.168.1.9:9393"
	req.Header.Set(hub.PagePortHeader, port)
	handler.ServeHTTP(httptest.NewRecorder(), req)
}

func hubLinkOf(t *testing.T, handler http.Handler) string {
	t.Helper()
	rec := get(handler, "/api/hub", "127.0.0.1:5000")
	var got hubLinkResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET /api/hub = %d, %v, want 200 with the link", rec.Code, err)
	}
	return got.URL
}

func TestRemembersTheHubsPage(t *testing.T) {
	for name, handler := range map[string]http.Handler{
		"data only": NewDataOnly(fakeCollector{}, nil, nil),
		"website":   newHandler(device(fakeCollector{}, nil), 0, site),
	} {
		t.Run(name, func(t *testing.T) {
			if got := hubLinkOf(t, handler); got != "" {
				t.Errorf("before a hub asked: link = %q, want none", got)
			}

			askAsHub(handler, "192.168.1.20:5000", "8090")
			if got, want := hubLinkOf(t, handler), "http://192.168.1.20:8090/"; got != want {
				t.Errorf("link = %q, want %q", got, want)
			}

			askAsHub(handler, "[fd00::20]:5000", "9393")
			if got, want := hubLinkOf(t, handler), "http://[fd00::20]:9393/"; got != want {
				t.Errorf("link = %q, want %q", got, want)
			}
		})
	}
}

func TestIgnoresInvalidHubPorts(t *testing.T) {
	handler := NewDataOnly(fakeCollector{}, nil, nil)
	askAsHub(handler, "192.168.1.20:5000", "8090")

	for _, port := range []string{"0", "65536", "-1", "80/evil", "port"} {
		askAsHub(handler, "192.168.1.30:5000", port)
	}
	if got, want := hubLinkOf(t, handler), "http://192.168.1.20:8090/"; got != want {
		t.Errorf("link = %q, want %q kept", got, want)
	}
}

func TestHubLinkOnlyForThisMachine(t *testing.T) {
	handler := NewDataOnly(fakeCollector{}, nil, nil)

	if rec := get(handler, "/api/hub", "192.168.1.20:5000"); rec.Code != http.StatusForbidden {
		t.Errorf("GET /api/hub from the network = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if rec := get(handler, "/api/hub", "[::1]:5000"); rec.Code != http.StatusOK {
		t.Errorf("GET /api/hub from ::1 = %d, want %d", rec.Code, http.StatusOK)
	}
}
