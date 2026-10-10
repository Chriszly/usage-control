package router

import (
	"crypto/md5" //nolint:gosec // the digest of the fake FRITZ!Box
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

const fritzDescription = `<?xml version="1.0"?>
<root xmlns="urn:dslforum-org:device-1-0">
<device>
<deviceType>urn:dslforum-org:device:InternetGatewayDevice:1</deviceType>
<serviceList>
<service><serviceType>urn:dslforum-org:service:DeviceInfo:1</serviceType><controlURL>/upnp/control/deviceinfo</controlURL></service>
<service><serviceType>urn:dslforum-org:service:Hosts:1</serviceType><controlURL>/upnp/control/hosts</controlURL></service>
</serviceList>
<deviceList>
<device>
<deviceType>urn:dslforum-org:device:WANDevice:1</deviceType>
<serviceList>
<service><serviceType>urn:dslforum-org:service:WANCommonInterfaceConfig:1</serviceType><controlURL>/upnp/control/wancommonifconfig1</controlURL></service>
<service><serviceType>urn:dslforum-org:service:WANDSLInterfaceConfig:1</serviceType><controlURL>/upnp/control/wandslifconfig1</controlURL></service>
</serviceList>
<deviceList>
<device>
<serviceList>
<service><serviceType>urn:dslforum-org:service:WANIPConnection:1</serviceType><controlURL>/upnp/control/wanipconnection1</controlURL></service>
<service><serviceType>urn:dslforum-org:service:WANPPPConnection:1</serviceType><controlURL>http://192.0.2.1:49000/upnp/control/wanpppconn1</controlURL></service>
</serviceList>
</device>
</deviceList>
</device>
</deviceList>
</device>
</root>`

const fakeHostList = `<?xml version="1.0"?><List>
<Item><Index>1</Index><Active>1</Active><InterfaceType>Ethernet</InterfaceType></Item>
<Item><Index>2</Index><Active>1</Active><InterfaceType>802.11</InterfaceType></Item>
<Item><Index>3</Index><Active>1</Active><InterfaceType>802.11</InterfaceType></Item>
<Item><Index>4</Index><Active>0</Active><InterfaceType>802.11</InterfaceType></Item>
</List>`

// fakeFritzBox answers TR-064 as a FRITZ!Box does, with an HTTP digest
// login for every action.
type fakeFritzBox struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	password string
	nonce    string
	lastNC   string
	// staleAfter makes the nonce stale after that many actions.
	staleAfter int
	actions    []string
	unauthed   int
	received   uint64
	uptime     uint64
}

func newFakeFritzBox(t *testing.T) *fakeFritzBox {
	t.Helper()
	f := &fakeFritzBox{t: t, password: "secret", nonce: "A1B2C3", received: 5 << 32, uptime: 7200}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeFritzBox) address() string {
	return strings.TrimPrefix(f.server.URL, "http://")
}

func (f *fakeFritzBox) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/tr64desc.xml":
		_, _ = io.WriteString(w, fritzDescription)
		return
	case r.Method == http.MethodGet && r.URL.Path == "/devicehostlist.lua":
		if r.URL.Query().Get("sid") != "0123" {
			http.Error(w, "no session", http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, fakeHostList)
		return
	}
	stale := f.staleAfter > 0 && len(f.actions) == f.staleAfter
	if !f.authorized(r) || stale {
		f.unauthed++
		if stale {
			// The count starts again with a new nonce.
			f.nonce, f.lastNC = f.nonce+"x", ""
			f.staleAfter = 0
		}
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Digest realm="F!Box SOAP-Auth", nonce="%s", algorithm=MD5, qop="auth"%s`,
			f.nonce, map[bool]string{true: ", stale=TRUE"}[stale]))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	action := strings.Trim(r.Header.Get("SOAPAction"), `"`)
	f.actions = append(f.actions, r.URL.Path+"#"+action[strings.Index(action, "#")+1:])
	var values string
	switch r.URL.Path + "#" + action[strings.Index(action, "#")+1:] {
	case "/upnp/control/wancommonifconfig1#GetAddonInfos":
		values = fmt.Sprintf("<NewByteSendRate>100</NewByteSendRate><NewTotalBytesSent>12</NewTotalBytesSent><NewX_AVM_DE_TotalBytesSent64>%d</NewX_AVM_DE_TotalBytesSent64><NewX_AVM_DE_TotalBytesReceived64>%d</NewX_AVM_DE_TotalBytesReceived64>", f.received/4, f.received)
	case "/upnp/control/wancommonifconfig1#GetCommonLinkProperties":
		values = "<NewWANAccessType>DSL</NewWANAccessType><NewLayer1UpstreamMaxBitRate>48000000</NewLayer1UpstreamMaxBitRate><NewLayer1DownstreamMaxBitRate>270000000</NewLayer1DownstreamMaxBitRate><NewPhysicalLinkStatus>Up</NewPhysicalLinkStatus>"
	case "/upnp/control/wandslifconfig1#GetInfo":
		values = "<NewStatus>Up</NewStatus><NewUpstreamCurrRate>40000</NewUpstreamCurrRate><NewDownstreamCurrRate>250000</NewDownstreamCurrRate><NewUpstreamNoiseMargin>80</NewUpstreamNoiseMargin><NewDownstreamNoiseMargin>65</NewDownstreamNoiseMargin>"
	case "/upnp/control/deviceinfo#GetInfo":
		values = fmt.Sprintf("<NewModelName>FRITZ!Box 7590</NewModelName><NewUpTime>%d</NewUpTime>", f.uptime)
	case "/upnp/control/wanipconnection1#GetStatusInfo":
		values = "<NewConnectionStatus>Connected</NewConnectionStatus><NewUptime>3600</NewUptime>"
	case "/upnp/control/hosts#X_AVM-DE_GetHostListPath":
		values = "<NewX_AVM-DE_HostListPath>/devicehostlist.lua?sid=0123</NewX_AVM-DE_HostListPath>"
	default:
		http.Error(w, "unknown action", http.StatusInternalServerError)
		return
	}
	_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:Response xmlns:u="x">%s</u:Response></s:Body></s:Envelope>`, values)
}

// authorized checks a digest login with MD5 and qop=auth, and that its
// count grows.
func (f *fakeFritzBox) authorized(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	scheme, rest, _ := strings.Cut(header, " ")
	if scheme != "Digest" {
		return false
	}
	p := digestParams(rest)
	if p["username"] != "monitor" || p["nonce"] != f.nonce || p["uri"] != r.URL.RequestURI() || p["qop"] != "auth" || p["nc"] <= f.lastNC {
		return false
	}
	sum := func(text string) string {
		h := md5.Sum([]byte(text)) //nolint:gosec // the digest of the fake FRITZ!Box
		return hex.EncodeToString(h[:])
	}
	ha1 := sum("monitor:" + p["realm"] + ":" + f.password)
	ha2 := sum(r.Method + ":" + r.URL.RequestURI())
	if p["response"] != sum(ha1+":"+f.nonce+":"+p["nc"]+":"+p["cnonce"]+":auth:"+ha2) {
		return false
	}
	f.lastNC = p["nc"]
	return true
}

func TestFritzBoxReadsTheBox(t *testing.T) {
	box := newFakeFritzBox(t)
	reader := NewFritzBox(box.address(), "monitor", "secret")
	snapshot, err := reader.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.UptimeSeconds != 7200 {
		t.Errorf("uptime %d", snapshot.UptimeSeconds)
	}
	if len(snapshot.Network) != 1 {
		t.Fatalf("network %+v", snapshot.Network)
	}
	wan := snapshot.Network[0]
	if wan.Name != "WAN" || wan.ReceivedBytes != 5<<32 || wan.SentBytes != 5<<30 || wan.LinkMbps != 250 {
		t.Errorf("WAN is %+v", wan)
	}
	values := extraValues(snapshot)
	want := map[string]string{
		"internet/state": "Connected", "internet/download": "31250000", "internet/upload": "5000000",
		"internet/connected-hours": "1", "internet/noise-margin-down": "6.5", "internet/noise-margin-up": "8",
		"clients/online": "3", "clients/wired": "1", "clients/wireless": "2",
	}
	for key, value := range want {
		if values[key] != value {
			t.Errorf("%s = %q, want %q", key, values[key], value)
		}
	}
	// The PPP connection's control URL is not on the box, so it is not
	// asked.
	for _, action := range box.actions {
		if strings.Contains(action, "wanppp") {
			t.Errorf("asked %s", action)
		}
	}
	// Only actions that read are called.
	for _, action := range box.actions {
		if _, name, _ := strings.Cut(action, "#"); !slices.Contains(FritzBoxActions, name) {
			t.Errorf("called %s, which is not in FritzBoxActions", action)
		}
	}
	// The login is only challenged once.
	if box.unauthed != 1 {
		t.Errorf("%d requests were challenged, want 1", box.unauthed)
	}

	box.mu.Lock()
	box.received += 1000
	box.mu.Unlock()
	reader.received.at = reader.received.at.Add(-10 * time.Second)
	snapshot, err = reader.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rate := snapshot.Network[0].ReceiveBytesPerSecond; rate < 99 || rate > 101 {
		t.Errorf("receive rate %v, want about 100", rate)
	}
}

func extraValues(snapshot metrics.Snapshot) map[string]string {
	values := map[string]string{}
	for _, extra := range snapshot.Extras {
		for _, item := range extra.Items {
			value := item.Text
			if item.Value != nil {
				value = strconv.FormatFloat(*item.Value, 'f', -1, 64)
			}
			values[extra.ID+"/"+item.ID] = value
		}
	}
	return values
}

func TestFritzBoxStaleNonce(t *testing.T) {
	box := newFakeFritzBox(t)
	box.staleAfter = 2
	reader := NewFritzBox(box.address(), "monitor", "secret")
	if _, err := reader.Collect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if box.unauthed != 2 {
		t.Errorf("%d requests were challenged, want 2", box.unauthed)
	}
}

func TestFritzBoxWrongPasswordBacksOff(t *testing.T) {
	box := newFakeFritzBox(t)
	reader := NewFritzBox(box.address(), "monitor", "wrong")
	if _, err := reader.Collect(t.Context()); err == nil || !strings.Contains(err.Error(), "refused the login") {
		t.Fatalf("got %v", err)
	}
	// One request to get the nonce, one with the wrong password.
	if box.unauthed != 2 {
		t.Errorf("%d requests were refused, want 2", box.unauthed)
	}
	if _, err := reader.Collect(t.Context()); err == nil {
		t.Fatal("a second reading logged in")
	}
	if box.unauthed != 2 {
		t.Errorf("the login was tried again at once: %d refused", box.unauthed)
	}
}

func TestFritzBoxWithoutTR064(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	_, err := NewFritzBox(strings.TrimPrefix(server.URL, "http://"), "monitor", "secret").Collect(t.Context())
	if err == nil || !strings.Contains(err.Error(), "TR-064") {
		t.Errorf("got %v", err)
	}
}

func TestDigestParams(t *testing.T) {
	got := digestParams(`realm="F!Box, SOAP-Auth", nonce="a\"b", algorithm=MD5, qop="auth,auth-int", stale=TRUE`)
	want := map[string]string{"realm": "F!Box, SOAP-Auth", "nonce": `a"b`, "algorithm": "MD5", "qop": "auth,auth-int", "stale": "TRUE"}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}
	var d digest
	if _, ok := d.challenge([]string{`Basic realm="x"`, `Digest realm="x", nonce="n", algorithm=SHA-512-256`}); ok {
		t.Error("an unknown algorithm was taken")
	}
	if stale, ok := d.challenge([]string{`Digest realm="x", nonce="n", qop="auth-int,auth", stale=true`}); !ok || !stale {
		t.Errorf("stale %v, ok %v", stale, ok)
	}
}
