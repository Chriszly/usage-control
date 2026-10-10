package router

import (
	"context"
	"crypto/md5" //nolint:gosec // HTTP digest with MD5 (RFC 7616), which FRITZ!OS asks for
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

const (
	// fritzPort is where a FRITZ!Box answers TR-064.
	fritzPort = "49000"
	// fritzInterval is how often a FRITZ!Box is read: each reading is several
	// requests with a login, so less often than a device.
	fritzInterval = 30 * time.Second
	// fritzLoginBackoff is how long a refused login is not tried again: a
	// FRITZ!Box blocks logins for a while after wrong ones, and logs each.
	fritzLoginBackoff = 5 * time.Minute
)

// The TR-064 services of a FRITZ!Box that are read; the version after the
// last colon varies.
const (
	fritzCommon     = "urn:dslforum-org:service:WANCommonInterfaceConfig:"
	fritzDSL        = "urn:dslforum-org:service:WANDSLInterfaceConfig:"
	fritzDeviceInfo = "urn:dslforum-org:service:DeviceInfo:"
	fritzHosts      = "urn:dslforum-org:service:Hosts:"
	fritzIP         = "urn:dslforum-org:service:WANIPConnection:"
	fritzPPP        = "urn:dslforum-org:service:WANPPPConnection:"
)

// FritzBoxActions are the only TR-064 actions a FRITZ!Box reader calls. Each
// only reads.
var FritzBoxActions = []string{"GetAddonInfos", "GetCommonLinkProperties", "GetInfo", "GetStatusInfo", "X_AVM-DE_GetHostListPath"}

// errFritzLogin is a login the FRITZ!Box refused.
var errFritzLogin = errors.New("the FRITZ!Box refused the login; check the user and password, and that access for applications (TR-064) is allowed in Home Network > Network > Network Settings")

// FritzBoxReader reads a FRITZ!Box over TR-064, its interface for
// applications, with a login: the internet traffic and line, the uptime and
// the clients in the home network.
type FritzBoxReader struct {
	host   string
	port   string
	client *http.Client
	digest digest

	mu sync.Mutex
	// services are the TR-064 services found in the description, by the
	// prefixes above; empty until it was read.
	services       map[string]upnpService
	routerIP       netip.Addr
	base           *url.URL
	received, sent counter
	uptime         uint64
	uptimeRead     bool
	refusedAt      time.Time
	refusedLogged  bool
}

// NewFritzBox returns a reader for the FRITZ!Box at address, a host name or
// IP address with an optional port, and the user and password of a FRITZ!Box
// user.
func NewFritzBox(address, user, password string) *FritzBoxReader {
	host, port := address, fritzPort
	if h, p, err := net.SplitHostPort(address); err == nil {
		host, port = h, p
	}
	return &FritzBoxReader{
		host: host, port: port, client: newClient(localNetworkOnly),
		digest: digest{user: user, password: password},
	}
}

// Collect reads the FRITZ!Box's usage.
func (f *FritzBoxReader) Collect(ctx context.Context) (metrics.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.refusedAt.IsZero() && time.Since(f.refusedAt) < fritzLoginBackoff {
		return metrics.Snapshot{}, errFritzLogin
	}
	if f.services == nil {
		if err := f.find(ctx); err != nil {
			return metrics.Snapshot{}, err
		}
	}
	snapshot, err := f.read(ctx)
	if errors.Is(err, errFritzLogin) {
		f.refusedAt = time.Now()
		if !f.refusedLogged {
			slog.Warn("a FRITZ!Box refused the login; it is tried again every few minutes", "router", f.host)
			f.refusedLogged = true
		}
		return metrics.Snapshot{}, err
	}
	if err != nil {
		// The FRITZ!Box may have restarted with a new description.
		f.services = nil
		return metrics.Snapshot{}, err
	}
	f.refusedAt, f.refusedLogged = time.Time{}, false
	return snapshot, nil
}

// find reads the FRITZ!Box's TR-064 description and the services in it.
func (f *FritzBoxReader) find(ctx context.Context) error {
	ip, err := localIP(ctx, f.host)
	if err != nil {
		return err
	}
	// The description is asked at the IP address, so its control URLs can be
	// checked to be on the FRITZ!Box itself.
	location := "http://" + net.JoinHostPort(ip.String(), f.port) + "/tr64desc.xml"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return err
	}
	response, err := f.client.Do(request)
	if err != nil {
		return fmt.Errorf("read the TR-064 description of %s: %w", f.host, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("read the TR-064 description of %s: answered %s; allow access for applications (TR-064) in Home Network > Network > Network Settings", f.host, response.Status)
	}
	data, err := readBody(response.Body)
	if err != nil {
		return fmt.Errorf("read the TR-064 description of %s: %w", f.host, err)
	}
	var description upnpDescription
	if err := xml.Unmarshal(data, &description); err != nil {
		return fmt.Errorf("read the TR-064 description of %s: %w", f.host, err)
	}
	base, _ := url.Parse(location)
	services := map[string]upnpService{}
	var walk func(device upnpDevice)
	walk = func(device upnpDevice) {
		for _, service := range device.Services {
			for _, prefix := range []string{fritzCommon, fritzDSL, fritzDeviceInfo, fritzHosts, fritzIP, fritzPPP} {
				if _, found := services[prefix]; found || !strings.HasPrefix(service.ServiceType, prefix) {
					continue
				}
				if control, err := sameHost(base, service.ControlURL, ip); err == nil {
					services[prefix] = upnpService{ServiceType: service.ServiceType, ControlURL: control}
				}
			}
		}
		for _, child := range device.Devices {
			walk(child)
		}
	}
	walk(description.Device)
	if _, ok := services[fritzCommon]; !ok {
		return fmt.Errorf("the TR-064 description of %s has no internet connection (WANCommonInterfaceConfig); is it a FRITZ!Box?", f.host)
	}
	f.services, f.routerIP, f.base = services, ip, base
	return nil
}

// read asks the services found before.
func (f *FritzBoxReader) read(ctx context.Context) (metrics.Snapshot, error) {
	addon, err := f.call(ctx, fritzCommon, "GetAddonInfos")
	if err != nil {
		return metrics.Snapshot{}, err
	}
	receivedBytes, receivedErr := strconv.ParseUint(addon["NewX_AVM_DE_TotalBytesReceived64"], 10, 64)
	sentBytes, sentErr := strconv.ParseUint(addon["NewX_AVM_DE_TotalBytesSent64"], 10, 64)
	rate := (*counter).rate64
	if receivedErr != nil || sentErr != nil {
		rate = (*counter).rate
		// Older FRITZ!OS versions only count in 32 bits.
		receivedBytes, receivedErr = strconv.ParseUint(addon["NewTotalBytesReceived"], 10, 64)
		sentBytes, sentErr = strconv.ParseUint(addon["NewTotalBytesSent"], 10, 64)
		if receivedErr != nil || sentErr != nil {
			return metrics.Snapshot{}, fmt.Errorf("the FRITZ!Box %s tells no internet traffic", f.host)
		}
	}
	// The rest is told by most models, but not all; what is missing is left
	// out. A refused login is not, as it would be refused again.
	optional := func(prefix, action string) (map[string]string, error) {
		if _, ok := f.services[prefix]; !ok {
			return nil, nil
		}
		values, err := f.call(ctx, prefix, action)
		if errors.Is(err, errFritzLogin) {
			return nil, err
		}
		return values, nil
	}
	link, err := optional(fritzCommon, "GetCommonLinkProperties")
	if err != nil {
		return metrics.Snapshot{}, err
	}
	info, err := optional(fritzDeviceInfo, "GetInfo")
	if err != nil {
		return metrics.Snapshot{}, err
	}
	dsl, err := optional(fritzDSL, "GetInfo")
	if err != nil {
		return metrics.Snapshot{}, err
	}
	// A DSL line connects over PPP, whose state counts then, also while it
	// is down; the IP connection is listed too, but not used.
	status, err := optional(fritzPPP, "GetStatusInfo")
	if err != nil {
		return metrics.Snapshot{}, err
	}
	if state := status["NewConnectionStatus"]; state == "" || state == "Unconfigured" {
		if status, err = optional(fritzIP, "GetStatusInfo"); err != nil {
			return metrics.Snapshot{}, err
		}
	}

	var snapshot metrics.Snapshot
	restarted := false
	if uptime, err := strconv.ParseUint(info["NewUpTime"], 10, 64); err == nil {
		snapshot.UptimeSeconds = uptime
		restarted = f.uptimeRead && uptime < f.uptime
		f.uptime, f.uptimeRead = uptime, true
	}
	// The line's speed: what a DSL line synchronized at, in kbit/s, or else
	// the link's, in bit/s.
	down, up := parseRate(dsl["NewDownstreamCurrRate"], 1000), parseRate(dsl["NewUpstreamCurrRate"], 1000)
	if down == 0 {
		down, up = parseRate(link["NewLayer1DownstreamMaxBitRate"], 1), parseRate(link["NewLayer1UpstreamMaxBitRate"], 1)
	}
	// Traffic faster than the line, with room for a line that synchronized
	// faster since, is a counter that was reset, not counted up.
	f.received.limit, f.sent.limit = float64(down)/8*2, float64(up)/8*2
	now := time.Now()
	wan := metrics.NetworkInterface{Name: "WAN", ReceivedBytes: receivedBytes, SentBytes: sentBytes}
	wan.ReceiveBytesPerSecond, _ = rate(&f.received, receivedBytes, now, restarted)
	wan.SendBytesPerSecond, _ = rate(&f.sent, sentBytes, now, restarted)

	if down > 0 {
		wan.LinkMbps = int(min(down/1_000_000, 1<<31-1))
	}
	snapshot.Network = []metrics.NetworkInterface{wan}

	var items []metrics.ExtraItem
	switch {
	case status["NewConnectionStatus"] != "":
		items = append(items, connectionState(status["NewConnectionStatus"]))
	case link["NewPhysicalLinkStatus"] != "":
		items = append(items, connectionState(link["NewPhysicalLinkStatus"]))
	}
	if down > 0 {
		items = append(items, bitRate("download", "Download speed of the line", map[string]string{
			"de": "Download-Geschwindigkeit der Leitung", "fr": "Débit descendant de la ligne", "es": "Velocidad de bajada de la línea",
		}, down))
	}
	if up > 0 {
		items = append(items, bitRate("upload", "Upload speed of the line", map[string]string{
			"de": "Upload-Geschwindigkeit der Leitung", "fr": "Débit montant de la ligne", "es": "Velocidad de subida de la línea",
		}, up))
	}
	if connected, err := strconv.ParseUint(status["NewUptime"], 10, 64); err == nil {
		hours := float64(connected) / 3600
		items = append(items, metrics.ExtraItem{
			ID: "connected-hours", Label: "Connected for (hours)", Unit: metrics.UnitNumber, Value: &hours,
			Labels: map[string]string{"de": "Verbunden seit (Stunden)", "fr": "Connecté depuis (heures)", "es": "Conectado desde hace (horas)"},
		})
	}
	// The noise margins of a DSL line, in tenths of a dB: the lower, the
	// closer the line is to losing its sync.
	for _, margin := range []struct{ id, value, label string }{
		{"noise-margin-down", "NewDownstreamNoiseMargin", "Noise margin down (dB)"},
		{"noise-margin-up", "NewUpstreamNoiseMargin", "Noise margin up (dB)"},
	} {
		tenths, err := strconv.ParseInt(dsl[margin.value], 10, 32)
		if err != nil || dsl["NewStatus"] != "Up" {
			continue
		}
		decibels := float64(tenths) / 10
		labels := map[string]string{"de": "Störabstand Download (dB)", "fr": "Marge de bruit descendante (dB)", "es": "Margen de ruido de bajada (dB)"}
		if margin.id == "noise-margin-up" {
			labels = map[string]string{"de": "Störabstand Upload (dB)", "fr": "Marge de bruit montante (dB)", "es": "Margen de ruido de subida (dB)"}
		}
		items = append(items, metrics.ExtraItem{ID: margin.id, Label: margin.label, Labels: labels, Unit: metrics.UnitNumber, Value: &decibels, History: true})
	}
	if len(items) > 0 {
		snapshot.Extras = append(snapshot.Extras, metrics.Extra{
			ID: "internet", Title: "Internet connection", Items: items,
			Titles: map[string]string{"de": "Internetverbindung", "fr": "Connexion Internet", "es": "Conexión a Internet"},
		})
	}
	if clients, ok := f.clients(ctx); ok {
		snapshot.Extras = append(snapshot.Extras, clients)
	}
	return snapshot, nil
}

// parseRate reads a rate in units of unit bits per second, or 0.
func parseRate(value string, unit uint64) uint64 {
	rate, err := strconv.ParseUint(value, 10, 64)
	if err != nil || rate > 1<<50/unit {
		return 0
	}
	return rate * unit
}

// fritzHostList is the list of the clients of a FRITZ!Box, which
// X_AVM-DE_GetHostListPath points at (FRITZ!OS 7.25 and later).
type fritzHostList struct {
	Items []struct {
		Active        string `xml:"Active"`
		InterfaceType string `xml:"InterfaceType"`
	} `xml:"Item"`
}

// clients counts the clients in the home network that are online, on a
// cable and on Wi-Fi.
func (f *FritzBoxReader) clients(ctx context.Context) (metrics.Extra, bool) {
	if _, ok := f.services[fritzHosts]; !ok {
		return metrics.Extra{}, false
	}
	path, err := f.call(ctx, fritzHosts, "X_AVM-DE_GetHostListPath")
	if err != nil {
		return metrics.Extra{}, false
	}
	location, err := sameHost(f.base, path["NewX_AVM-DE_HostListPath"], f.routerIP)
	if err != nil {
		return metrics.Extra{}, false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return metrics.Extra{}, false
	}
	response, err := f.client.Do(request)
	if err != nil {
		return metrics.Extra{}, false
	}
	defer func() { _ = response.Body.Close() }()
	data, err := readBody(response.Body)
	var list fritzHostList
	if err != nil || response.StatusCode != http.StatusOK || xml.Unmarshal(data, &list) != nil {
		return metrics.Extra{}, false
	}
	var wired, wireless float64
	for _, item := range list.Items {
		if item.Active != "1" {
			continue
		}
		if strings.HasPrefix(item.InterfaceType, "802.11") {
			wireless++
		} else {
			wired++
		}
	}
	return clientsExtra(wired, wireless), true
}

// call calls a TR-064 action that only reads, logged in with HTTP digest,
// and returns the values in its answer by name.
func (f *FritzBoxReader) call(ctx context.Context, prefix, action string) (map[string]string, error) {
	service := f.services[prefix]
	// The first request of a login, or one with a nonce the FRITZ!Box no
	// longer takes, is answered with a new nonce and sent again.
	for attempt := 0; ; attempt++ {
		request, err := soapRequest(ctx, service, action)
		if err != nil {
			return nil, err
		}
		f.digest.authorize(request)
		response, err := f.client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("ask %s for %s: %w", f.host, action, err)
		}
		if response.StatusCode != http.StatusUnauthorized {
			values, err := soapAnswer(response, f.host, action)
			_ = response.Body.Close()
			return values, err
		}
		_ = response.Body.Close()
		stale, ok := f.digest.challenge(response.Header.Values("WWW-Authenticate"))
		if !ok {
			return nil, fmt.Errorf("ask %s for %s: it asks for a login other than HTTP digest", f.host, action)
		}
		if (attempt > 0 && !stale) || attempt > 1 {
			// The next login starts without the refused password, so it
			// costs one failed login, not two.
			f.digest.nonce = ""
			return nil, errFritzLogin
		}
	}
}

// digest logs in with HTTP digest authentication (RFC 7616), with MD5 or
// SHA-256 and qop=auth, as a FRITZ!Box asks for.
type digest struct {
	user, password string

	realm, nonce, opaque, algorithm string
	qop                             bool
	count                           uint32
}

// authorize adds the login to a request, once a challenge gave a nonce.
func (d *digest) authorize(request *http.Request) {
	if d.nonce == "" {
		return
	}
	d.count++
	newHash := md5.New
	if d.algorithm == "SHA-256" {
		newHash = sha256.New
	}
	sum := func(parts ...string) string {
		h := newHash()
		_, _ = h.Write([]byte(strings.Join(parts, ":")))
		return hex.EncodeToString(h.Sum(nil))
	}
	uri := request.URL.RequestURI()
	ha1 := sum(d.user, d.realm, d.password)
	ha2 := sum(request.Method, uri)
	header := fmt.Sprintf(`Digest username=%s, realm=%s, nonce=%s, uri=%s`, quote(d.user), quote(d.realm), quote(d.nonce), quote(uri))
	if d.qop {
		var random [12]byte
		_, _ = rand.Read(random[:])
		cnonce := hex.EncodeToString(random[:])
		nc := fmt.Sprintf("%08x", d.count)
		header += fmt.Sprintf(`, qop=auth, nc=%s, cnonce=%s, response=%s`, nc, quote(cnonce), quote(sum(ha1, d.nonce, nc, cnonce, "auth", ha2)))
	} else {
		header += `, response=` + quote(sum(ha1, d.nonce, ha2))
	}
	if d.algorithm != "" {
		header += ", algorithm=" + d.algorithm
	}
	if d.opaque != "" {
		header += ", opaque=" + quote(d.opaque)
	}
	request.Header.Set("Authorization", header)
}

// challenge takes the nonce of a digest challenge, and reports whether the
// nonce before was only stale, not the login refused.
func (d *digest) challenge(headers []string) (stale, ok bool) {
	for _, header := range headers {
		scheme, rest, _ := strings.Cut(strings.TrimSpace(header), " ")
		if !strings.EqualFold(scheme, "Digest") {
			continue
		}
		params := digestParams(rest)
		algorithm := strings.ToUpper(params["algorithm"])
		if params["nonce"] == "" || (algorithm != "" && algorithm != "MD5" && algorithm != "SHA-256") {
			continue
		}
		qop := params["qop"]
		if qop != "" && !strings.Contains(","+strings.ReplaceAll(qop, " ", "")+",", ",auth,") {
			continue
		}
		d.realm, d.nonce, d.opaque, d.algorithm = params["realm"], params["nonce"], params["opaque"], algorithm
		d.qop, d.count = qop != "", 0
		return strings.EqualFold(params["stale"], "true"), true
	}
	return false, false
}

// digestParams reads the parameters of a challenge, such as
// realm="F!Box SOAP-Auth", nonce="1A2B", algorithm=MD5, qop="auth".
func digestParams(text string) map[string]string {
	params := map[string]string{}
	for text != "" {
		text = strings.TrimLeft(text, " ,")
		name, rest, ok := strings.Cut(text, "=")
		if !ok {
			break
		}
		name = strings.ToLower(strings.TrimSpace(name))
		var value string
		if strings.HasPrefix(rest, `"`) {
			var b strings.Builder
			i := 1
			for ; i < len(rest) && rest[i] != '"'; i++ {
				if rest[i] == '\\' && i+1 < len(rest) {
					i++
				}
				b.WriteByte(rest[i])
			}
			value, text = b.String(), rest[min(i+1, len(rest)):]
		} else {
			value, text, _ = strings.Cut(rest, ",")
			value = strings.TrimSpace(value)
		}
		params[name] = value
	}
	return params
}

// quote quotes a value of a digest header.
func quote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}
