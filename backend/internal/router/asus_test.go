package router

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeASUSRouter answers like the web interface of an ASUS router: a
// login with the right user and password gets a token, which appGet.cgi
// and ajax_coretmp.asp take until it is expired.
type fakeASUSRouter struct {
	server *httptest.Server

	mu      sync.Mutex
	paths   []string
	logins  int
	token   string
	appGet  string
	coretmp string
}

func newFakeASUSRouter(t *testing.T) *fakeASUSRouter {
	t.Helper()
	appGet, err := os.ReadFile("testdata/asus/appGet.json")
	if err != nil {
		t.Fatal(err)
	}
	coretmp, err := os.ReadFile("testdata/asus/coretmp.asp")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeASUSRouter{appGet: string(appGet), coretmp: string(coretmp)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.paths = append(f.paths, r.URL.Path)
		if r.UserAgent() != asusUserAgent {
			http.Error(w, "the login page", http.StatusOK)
			return
		}
		if r.URL.Path == "/login.cgi" {
			f.logins++
			_ = r.ParseForm()
			if r.PostForm.Get("login_authorization") != base64.StdEncoding.EncodeToString([]byte("admin:right")) {
				_, _ = w.Write([]byte(`{"error_status":"3"}`))
				return
			}
			f.token = "token" + strings.Repeat("x", f.logins)
			_, _ = w.Write([]byte(`{"asus_token":"` + f.token + `"}`))
			return
		}
		if cookie, err := r.Cookie("asus_token"); err != nil || f.token == "" || cookie.Value != f.token {
			_, _ = w.Write([]byte(`<html>the login page</html>`))
			return
		}
		switch r.URL.Path {
		case "/appGet.cgi":
			_ = r.ParseForm()
			if r.PostForm.Get("hook") != asusHooks {
				http.Error(w, "unknown hook", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(f.appGet))
		case "/ajax_coretmp.asp":
			_, _ = w.Write([]byte(f.coretmp))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeASUSRouter) reader(password string) *ASUSReader {
	return NewASUS(strings.TrimPrefix(f.server.URL, "http://"), "admin", password)
}

func TestASUSReadsTheRouter(t *testing.T) {
	router := newFakeASUSRouter(t)
	reader := router.reader("right")

	snapshot, err := reader.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.UptimeSeconds != 86400 {
		t.Errorf("uptime %d", snapshot.UptimeSeconds)
	}
	if snapshot.CPU.Cores != 2 || snapshot.CPU.CoreUsagePercent[0] != 10 || snapshot.CPU.CoreUsagePercent[1] != 30 || snapshot.CPU.UsagePercent != 20 {
		t.Errorf("CPU %+v", snapshot.CPU)
	}
	if snapshot.Memory.TotalBytes != 512<<20 || snapshot.Memory.UsedPercent != 50 {
		t.Errorf("memory %+v", snapshot.Memory)
	}
	var names []string
	for _, network := range snapshot.Network {
		names = append(names, network.Name)
	}
	if want := []string{"WAN", "LAN", "Wi-Fi 2.4 GHz", "Wi-Fi 5 GHz"}; !slices.Equal(names, want) {
		t.Errorf("interfaces %q, want %q", names, want)
	}
	if snapshot.Network[0].ReceivedBytes != 0x100000 {
		t.Errorf("WAN %+v", snapshot.Network[0])
	}
	if online := extraItem(snapshot, "clients", "online"); online == nil || *online.Value != 2 {
		t.Errorf("clients online %+v", online)
	}
	if wireless := extraItem(snapshot, "clients", "wireless"); wireless == nil || *wireless.Value != 1 {
		t.Errorf("clients on Wi-Fi %+v", wireless)
	}
	var sensors []string
	for _, temperature := range snapshot.Temperatures {
		sensors = append(sensors, temperature.Sensor)
	}
	if want := []string{"Wi-Fi 2.4 GHz", "Wi-Fi 5 GHz", "CPU"}; !slices.Equal(sensors, want) {
		t.Errorf("temperatures %+v, want %q", snapshot.Temperatures, want)
	}

	// The next reading measures the CPU and traffic since the first.
	router.mu.Lock()
	router.appGet = strings.NewReplacer(
		`"cpu1_total":"1000","cpu1_usage":"100"`, `"cpu1_total":"2000","cpu1_usage":"600"`,
		`"INTERNET_rx":"0x100000"`, `"INTERNET_rx":"0x200000"`,
	).Replace(router.appGet)
	router.mu.Unlock()
	snapshot, err = reader.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CPU.CoreUsagePercent[0] != 50 || snapshot.CPU.CoreUsagePercent[1] != 0 {
		t.Errorf("CPU since the first reading %+v", snapshot.CPU)
	}
	if snapshot.Network[0].ReceiveBytesPerSecond <= 0 {
		t.Errorf("no traffic measured %+v", snapshot.Network[0])
	}

	router.mu.Lock()
	defer router.mu.Unlock()
	if router.logins != 1 {
		t.Errorf("logged in %d times, want once with the token kept", router.logins)
	}
	for _, path := range router.paths {
		if !slices.Contains(ASUSPaths, path) {
			t.Errorf("asked %s, which is not one of the pages that only read", path)
		}
	}
}

func TestASUSLogsInAgainWhenTheTokenExpired(t *testing.T) {
	router := newFakeASUSRouter(t)
	reader := router.reader("right")
	if _, err := reader.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	router.mu.Lock()
	router.token = "expired"
	router.mu.Unlock()
	if _, err := reader.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	if router.logins != 2 {
		t.Errorf("logged in %d times, want 2", router.logins)
	}
}

func TestASUSDoesNotRetryAWrongPasswordAtOnce(t *testing.T) {
	router := newFakeASUSRouter(t)
	reader := router.reader("wrong")
	for range 3 {
		if _, err := reader.Collect(t.Context()); err == nil {
			t.Fatal("a wrong password was taken")
		}
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	if router.logins != 1 {
		t.Errorf("tried to log in %d times, want once until the backoff is over", router.logins)
	}
}

func TestASUSInterfaceNames(t *testing.T) {
	for name, want := range map[string]string{
		"INTERNET": "WAN", "INTERNET1": "WAN 1", "WIRED": "LAN", "BRIDGE": "",
		"WIRELESS0": "Wi-Fi 2.4 GHz", "WIRELESS1": "Wi-Fi 5 GHz", "WIRELESS2": "Wi-Fi 3", "LACP": "LACP",
	} {
		if got := asusInterfaceName(name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}
