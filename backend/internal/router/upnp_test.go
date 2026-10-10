package router

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// fakeUPnPRouter answers a UPnP search on a UDP port of 127.0.0.1 with the
// location of a description served over HTTP, and the actions of its
// services with values that grow with each call.
type fakeUPnPRouter struct {
	t        *testing.T
	ssdp     *net.UDPConn
	web      *httptest.Server
	location string

	mu      sync.Mutex
	actions []string
	// descriptions counts the reads of the description; fail makes every
	// action fail.
	descriptions int
	fail         bool
	received     uint64
	sent         uint64
	uptime       uint64
}

func newFakeUPnPRouter(t *testing.T) *fakeUPnPRouter {
	t.Helper()
	description, err := os.ReadFile("testdata/upnp/description.xml")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeUPnPRouter{t: t, received: 1<<32 - 4000, sent: 5000, uptime: 600}
	f.web = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/rootDesc.xml" {
			f.mu.Lock()
			f.descriptions++
			f.mu.Unlock()
			_, _ = w.Write(description)
			return
		}
		body, _ := io.ReadAll(r.Body)
		action := strings.Trim(r.Header.Get("SOAPAction"), `"`)
		_, name, _ := strings.Cut(action, "#")
		f.mu.Lock()
		defer f.mu.Unlock()
		f.actions = append(f.actions, name)
		if !strings.Contains(string(body), "<u:"+name+" ") {
			t.Errorf("the body of %s does not call it: %s", name, body)
		}
		var values string
		switch {
		case f.fail:
			http.Error(w, "action failed", http.StatusInternalServerError)
			return
		case r.URL.Path == "/ctl/CmnIfCfg" && name == "GetTotalBytesReceived":
			values = fmt.Sprintf("<NewTotalBytesReceived>%d</NewTotalBytesReceived>", f.received)
		case r.URL.Path == "/ctl/CmnIfCfg" && name == "GetTotalBytesSent":
			values = fmt.Sprintf("<NewTotalBytesSent>%d</NewTotalBytesSent>", f.sent)
		case r.URL.Path == "/ctl/CmnIfCfg" && name == "GetCommonLinkProperties":
			values = "<NewWANAccessType>Ethernet</NewWANAccessType><NewLayer1UpstreamMaxBitRate>40000000</NewLayer1UpstreamMaxBitRate>" +
				"<NewLayer1DownstreamMaxBitRate>250000000</NewLayer1DownstreamMaxBitRate><NewPhysicalLinkStatus>Up</NewPhysicalLinkStatus>"
		case r.URL.Path == "/ctl/IPConn" && name == "GetStatusInfo":
			values = fmt.Sprintf("<NewConnectionStatus>Connected</NewConnectionStatus><NewLastConnectionError>ERROR_NONE</NewLastConnectionError><NewUptime>%d</NewUptime>", f.uptime)
		default:
			http.Error(w, "unknown action", http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:%sResponse xmlns:u="x">%s</u:%sResponse></s:Body></s:Envelope>`, name, values, name) //nolint:gosec // the answer of a fake router in a test
	}))
	t.Cleanup(f.web.Close)
	f.location = f.web.URL + "/rootDesc.xml"

	f.ssdp, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.ssdp.Close() })
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, from, err := f.ssdp.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			if !strings.HasPrefix(string(buffer[:n]), "M-SEARCH * HTTP/1.1\r\n") {
				continue
			}
			f.mu.Lock()
			location := f.location
			f.mu.Unlock()
			answer := "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=120\r\nST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\nEXT:\r\nLOCATION: " + location + "\r\n\r\n"
			_, _ = f.ssdp.WriteToUDP([]byte(answer), from)
		}
	}()
	return f
}

func (f *fakeUPnPRouter) reader() *UPnPReader {
	reader := NewUPnP("127.0.0.1")
	reader.ssdpPort = f.ssdp.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	reader.multicast = ""
	return reader
}

func TestUPnPReadsTheInternetTraffic(t *testing.T) {
	router := newFakeUPnPRouter(t)
	reader := router.reader()

	first, err := reader.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Network) != 1 || first.Network[0].Name != "WAN" || first.Network[0].LinkMbps != 250 {
		t.Fatalf("got %+v", first.Network)
	}
	if first.CPU.Cores != 0 || first.Memory.TotalBytes != 0 {
		t.Errorf("UPnP tells no CPU or memory, got %+v %+v", first.CPU, first.Memory)
	}
	state := extraItem(first, "internet", "state")
	if state == nil || state.Text != "Connected" {
		t.Errorf("connection state %+v", state)
	}
	if download := extraItem(first, "internet", "download"); download == nil || *download.Value != 250e6/8 {
		t.Errorf("download speed %+v", download)
	}

	// The received bytes wrap around their 32 bits.
	router.mu.Lock()
	router.received, router.sent, router.uptime = 4000, 7000, 605
	router.mu.Unlock()
	second, err := reader.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wan := second.Network[0]
	if wan.ReceiveBytesPerSecond <= 0 || wan.SendBytesPerSecond <= 0 {
		t.Errorf("no traffic measured: %+v", wan)
	}

	// After a restart of the connection, the interval is skipped.
	router.mu.Lock()
	router.received, router.sent, router.uptime = 100, 100, 3
	router.mu.Unlock()
	third, err := reader.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if third.Network[0].ReceiveBytesPerSecond != 0 {
		t.Errorf("traffic measured across a restart: %+v", third.Network[0])
	}

	router.mu.Lock()
	defer router.mu.Unlock()
	for _, action := range router.actions {
		if !slices.Contains(UPnPActions, action) {
			t.Errorf("called %s, which is not one of the actions that only read", action)
		}
	}
}

func TestUPnPDoesNotFollowADescriptionElsewhere(t *testing.T) {
	router := newFakeUPnPRouter(t)
	router.mu.Lock()
	router.location = strings.Replace(router.location, "127.0.0.1", "127.0.0.2", 1)
	router.mu.Unlock()
	if _, err := router.reader().Collect(t.Context()); err == nil {
		t.Error("a description on another host was read")
	}
}

func TestUPnPRefusesARouterOutsideTheLocalNetwork(t *testing.T) {
	if _, err := NewUPnP("8.8.8.8").Collect(t.Context()); err == nil || !strings.Contains(err.Error(), "local network") {
		t.Errorf("got %v", err)
	}
}

func TestSoapValues(t *testing.T) {
	values, err := soapValues([]byte(`<?xml version="1.0"?><s:Envelope xmlns:s="x"><s:Body><u:R xmlns:u="y"><NewA> 1 </NewA><Other>2</Other><NewB></NewB></u:R></s:Body></s:Envelope>`))
	if err != nil {
		t.Fatal(err)
	}
	if values["NewA"] != "1" || values["NewB"] != "" || len(values) != 2 {
		t.Errorf("got %q", values)
	}
}

func extraItem(snapshot metrics.Snapshot, group, item string) *metrics.ExtraItem {
	for _, extra := range snapshot.Extras {
		if extra.ID != group {
			continue
		}
		for i := range extra.Items {
			if extra.Items[i].ID == item {
				return &extra.Items[i]
			}
		}
	}
	return nil
}

func TestUPnPDoesNotSearchAgainAtOnce(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	// A router that never answers a search.
	reader := NewUPnP("127.0.0.1")
	reader.ssdpPort = uint16(conn.LocalAddr().(*net.UDPAddr).Port) //nolint:gosec // a port
	reader.multicast = ""
	if _, err := reader.Collect(t.Context()); err == nil {
		t.Fatal("found a router that does not answer")
	}
	start := time.Now()
	if _, err := reader.Collect(t.Context()); err == nil || !strings.Contains(err.Error(), "does not answer a UPnP search") {
		t.Errorf("got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Error("searched again at once")
	}
}

func TestUPnPDoesNotSearchAgainAtOnceAfterAFailedRead(t *testing.T) {
	router := newFakeUPnPRouter(t)
	router.mu.Lock()
	router.fail = true
	router.mu.Unlock()
	reader := router.reader()
	if _, err := reader.Collect(t.Context()); err == nil {
		t.Fatal("read a router whose actions fail")
	}
	if _, err := reader.Collect(t.Context()); err == nil {
		t.Fatal("read a router whose actions fail")
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	if router.descriptions != 1 {
		t.Errorf("read the description %d times, want once", router.descriptions)
	}
}
