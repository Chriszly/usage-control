package router

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/lan"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

const (
	// ssdpPort is where routers answer a search for UPnP devices.
	ssdpPort = 1900
	// ssdpMulticast is where a search is sent to every UPnP device on the
	// local network, for a router that does not answer one sent to it alone.
	ssdpMulticast = "239.255.255.250:1900"
	// ssdpWait is how long answers to a search are waited for.
	ssdpWait = 2 * time.Second
	// upnpSearchBackoff is how long a router that was not found is not
	// searched for again.
	upnpSearchBackoff = time.Minute
)

// The UPnP services that are read; the version after the last colon varies.
const (
	serviceCommon = "urn:schemas-upnp-org:service:WANCommonInterfaceConfig:"
	serviceIP     = "urn:schemas-upnp-org:service:WANIPConnection:"
	servicePPP    = "urn:schemas-upnp-org:service:WANPPPConnection:"
)

// UPnPActions are the only actions a UPnP reader calls. Each only reads.
var UPnPActions = []string{"GetTotalBytesReceived", "GetTotalBytesSent", "GetCommonLinkProperties", "GetStatusInfo"}

// UPnPReader reads the internet traffic of a router over UPnP, without a
// login: it finds the router's UPnP description with a search sent to the
// router, and asks the services of its internet connection.
type UPnPReader struct {
	host   string
	client *http.Client
	// ssdpPort is the router's port the search is sent to, and multicast
	// where it is sent to every device too; replaced in tests.
	ssdpPort  uint16
	multicast string

	mu sync.Mutex
	// common and connection are the control URLs of the services, and their
	// types, found in the description; empty until it was read.
	common, connection upnpService
	// notFound is when the router was last looked for in vain, and why: it
	// is looked for again only after upnpSearchBackoff, as each search goes
	// to every device on the local network.
	notFoundAt           time.Time
	notFound             error
	received, sent       counter
	connectionUptime     uint64
	connectionUptimeRead bool
}

// NewUPnP returns a reader for the router at host, a host name or IP
// address.
func NewUPnP(host string) *UPnPReader {
	return &UPnPReader{host: host, client: newClient(localNetworkOnly), ssdpPort: ssdpPort, multicast: ssdpMulticast}
}

// Collect reads the router's internet traffic and connection.
func (u *UPnPReader) Collect(ctx context.Context) (metrics.Snapshot, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.common.ControlURL == "" {
		if !u.notFoundAt.IsZero() && time.Since(u.notFoundAt) < upnpSearchBackoff {
			return metrics.Snapshot{}, u.notFound
		}
		if err := u.find(ctx); err != nil {
			u.notFoundAt, u.notFound = time.Now(), err
			return metrics.Snapshot{}, err
		}
		u.notFoundAt, u.notFound = time.Time{}, nil
	}
	snapshot, err := u.read(ctx)
	if err != nil {
		// The router may have restarted with its services elsewhere, so
		// they are looked for again next time.
		u.common, u.connection = upnpService{}, upnpService{}
		return metrics.Snapshot{}, err
	}
	return snapshot, nil
}

// read asks the services found before.
func (u *UPnPReader) read(ctx context.Context) (metrics.Snapshot, error) {
	received, err := u.call(ctx, u.common, "GetTotalBytesReceived")
	if err != nil {
		return metrics.Snapshot{}, err
	}
	sent, err := u.call(ctx, u.common, "GetTotalBytesSent")
	if err != nil {
		return metrics.Snapshot{}, err
	}
	receivedBytes, err := strconv.ParseUint(received["NewTotalBytesReceived"], 10, 64)
	if err != nil {
		return metrics.Snapshot{}, fmt.Errorf("read the bytes received from %s: %w", u.host, err)
	}
	sentBytes, err := strconv.ParseUint(sent["NewTotalBytesSent"], 10, 64)
	if err != nil {
		return metrics.Snapshot{}, fmt.Errorf("read the bytes sent from %s: %w", u.host, err)
	}
	// The link and the connection are told by most routers, but not all;
	// the traffic is shown without them.
	link, _ := u.call(ctx, u.common, "GetCommonLinkProperties")
	var status map[string]string
	if u.connection.ControlURL != "" {
		status, _ = u.call(ctx, u.connection, "GetStatusInfo")
	}

	// A connection that is up for less time than before was made anew,
	// usually as the router restarted, which may have reset the counters.
	restarted := false
	uptime, uptimeErr := strconv.ParseUint(status["NewUptime"], 10, 64)
	if uptimeErr == nil {
		restarted = u.connectionUptimeRead && uptime < u.connectionUptime
		u.connectionUptime, u.connectionUptimeRead = uptime, true
	}
	now := time.Now()
	down, downErr := strconv.ParseUint(link["NewLayer1DownstreamMaxBitRate"], 10, 64)
	up, upErr := strconv.ParseUint(link["NewLayer1UpstreamMaxBitRate"], 10, 64)
	// Traffic faster than the line, with room for a line that synchronized
	// faster since, is a counter that was reset, not counted up.
	u.received.limit, u.sent.limit = 0, 0
	if downErr == nil && down > 0 {
		u.received.limit = float64(down) / 8 * 2
	}
	if upErr == nil && up > 0 {
		u.sent.limit = float64(up) / 8 * 2
	}
	wan := metrics.NetworkInterface{Name: "WAN", ReceivedBytes: receivedBytes, SentBytes: sentBytes}
	wan.ReceiveBytesPerSecond, _ = u.received.rate(receivedBytes, now, restarted)
	wan.SendBytesPerSecond, _ = u.sent.rate(sentBytes, now, restarted)
	if downErr == nil && down > 0 {
		wan.LinkMbps = int(down / 1_000_000)
	}
	snapshot := metrics.Snapshot{Network: []metrics.NetworkInterface{wan}}

	var items []metrics.ExtraItem
	if state := status["NewConnectionStatus"]; state != "" {
		items = append(items, connectionState(state))
	} else if state := link["NewPhysicalLinkStatus"]; state != "" {
		items = append(items, connectionState(state))
	}
	if downErr == nil && down > 0 {
		items = append(items, bitRate("download", "Download speed of the line", map[string]string{
			"de": "Download-Geschwindigkeit der Leitung", "fr": "Débit descendant de la ligne", "es": "Velocidad de bajada de la línea",
		}, down))
	}
	if upErr == nil && up > 0 {
		items = append(items, bitRate("upload", "Upload speed of the line", map[string]string{
			"de": "Upload-Geschwindigkeit der Leitung", "fr": "Débit montant de la ligne", "es": "Velocidad de subida de la línea",
		}, up))
	}
	if uptimeErr == nil {
		hours := float64(uptime) / 3600
		items = append(items, metrics.ExtraItem{
			ID: "connected-hours", Label: "Connected for (hours)", Unit: metrics.UnitNumber, Value: &hours,
			Labels: map[string]string{"de": "Verbunden seit (Stunden)", "fr": "Connecté depuis (heures)", "es": "Conectado desde hace (horas)"},
		})
	}
	if len(items) > 0 {
		snapshot.Extras = []metrics.Extra{{
			ID: "internet", Title: "Internet connection", Items: items,
			Titles: map[string]string{"de": "Internetverbindung", "fr": "Connexion Internet", "es": "Conexión a Internet"},
		}}
	}
	return snapshot, nil
}

// connectionState is the state of the internet connection as a text value,
// such as "Connected" or "Up".
func connectionState(state string) metrics.ExtraItem {
	return metrics.ExtraItem{
		ID: "state", Label: "State", Unit: metrics.UnitText, Text: state,
		Labels: map[string]string{"de": "Zustand", "fr": "État", "es": "Estado"},
	}
}

// bitRate is a speed of the line in bits per second, as bytes per second.
func bitRate(id, label string, labels map[string]string, bitsPerSecond uint64) metrics.ExtraItem {
	bytesPerSecond := float64(bitsPerSecond) / 8
	return metrics.ExtraItem{ID: id, Label: label, Labels: labels, Unit: metrics.UnitBytesPerSecond, Value: &bytesPerSecond}
}

// upnpService is a service in a router's UPnP description.
type upnpService struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

type upnpDevice struct {
	Services []upnpService `xml:"serviceList>service"`
	Devices  []upnpDevice  `xml:"deviceList>device"`
}

type upnpDescription struct {
	URLBase string     `xml:"URLBase"`
	Device  upnpDevice `xml:"device"`
}

// find looks for the router's UPnP description and the services in it.
func (u *UPnPReader) find(ctx context.Context) error {
	routerIP, err := localIP(ctx, u.host)
	if err != nil {
		return err
	}
	location, err := u.search(ctx, routerIP)
	if err != nil {
		return err
	}
	description, base, err := u.description(ctx, location, routerIP)
	if err != nil {
		return err
	}
	var common, connection upnpService
	var walk func(device upnpDevice)
	walk = func(device upnpDevice) {
		for _, service := range device.Services {
			switch {
			case strings.HasPrefix(service.ServiceType, serviceCommon) && common.ControlURL == "":
				common = service
			case (strings.HasPrefix(service.ServiceType, serviceIP) || strings.HasPrefix(service.ServiceType, servicePPP)) && connection.ControlURL == "":
				connection = service
			}
		}
		for _, child := range device.Devices {
			walk(child)
		}
	}
	walk(description.Device)
	if common.ControlURL == "" {
		return fmt.Errorf("the UPnP description of %s has no internet connection (WANCommonInterfaceConfig)", u.host)
	}
	if common.ControlURL, err = sameHost(base, common.ControlURL, routerIP); err != nil {
		return err
	}
	if connection.ControlURL != "" {
		if connection.ControlURL, err = sameHost(base, connection.ControlURL, routerIP); err != nil {
			connection = upnpService{}
		}
	}
	u.common, u.connection = common, connection
	return nil
}

// localIP returns the IP address of the router at host, a host name or IP
// address, which must be on the local network.
func localIP(ctx context.Context, host string) (netip.Addr, error) {
	name := host
	host = strings.Trim(host, "[]")
	ip, err := netip.ParseAddr(host)
	if err != nil {
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("look up the router %s: %w", name, err)
		}
		if len(ips) == 0 {
			return netip.Addr{}, fmt.Errorf("look up the router %s: no address", name)
		}
		ip = ips[0]
	}
	ip = ip.Unmap()
	if !lan.Default.Local(ip) {
		return netip.Addr{}, fmt.Errorf("the router %s is not on the local network", name)
	}
	return ip, nil
}

// search sends a UPnP search to the router, and to every device on the
// local network for a router that only answers that, and returns the URL of
// the description the router answers with. Answers of other devices are
// ignored.
func (u *UPnPReader) search(ctx context.Context, routerIP netip.Addr) (string, error) {
	network := "udp4"
	if routerIP.Is6() {
		network = "udp6"
	}
	conn, err := net.ListenUDP(network, nil)
	if err != nil {
		return "", fmt.Errorf("search for the router's UPnP: %w", err)
	}
	defer func() { _ = conn.Close() }()
	deadline := time.Now().Add(ssdpWait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	targets := []*net.UDPAddr{net.UDPAddrFromAddrPort(netip.AddrPortFrom(routerIP, u.ssdpPort))}
	if routerIP.Is4() && u.multicast != "" {
		if multicast, err := net.ResolveUDPAddr("udp4", u.multicast); err == nil {
			targets = append(targets, multicast)
		}
	}
	for _, searched := range []string{"urn:schemas-upnp-org:device:InternetGatewayDevice:1", "urn:schemas-upnp-org:device:InternetGatewayDevice:2"} {
		message := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: " + searched + "\r\n\r\n"
		for _, target := range targets {
			// A multicast search fails where there is no route for it, as
			// in a container; the one sent to the router alone counts.
			_, _ = conn.WriteToUDP([]byte(message), target)
		}
	}

	buffer := make([]byte, 4096)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buffer)
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				return "", fmt.Errorf("the router %s does not answer a UPnP search; switch on UPnP in its settings", u.host)
			}
			return "", fmt.Errorf("search for the router's UPnP: %w", err)
		}
		if from.Addr().Unmap() != routerIP {
			continue
		}
		response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(buffer[:n])), nil)
		if err != nil {
			continue
		}
		_ = response.Body.Close()
		location := response.Header.Get("Location")
		if _, err := sameHost(nil, location, routerIP); err == nil {
			return location, nil
		}
	}
}

// description reads the UPnP description at location, and returns it with
// the URL its control URLs are relative to.
func (u *UPnPReader) description(ctx context.Context, location string, routerIP netip.Addr) (upnpDescription, *url.URL, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return upnpDescription{}, nil, err
	}
	response, err := u.client.Do(request)
	if err != nil {
		return upnpDescription{}, nil, fmt.Errorf("read the UPnP description of %s: %w", u.host, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return upnpDescription{}, nil, fmt.Errorf("read the UPnP description of %s: answered %s", u.host, response.Status)
	}
	data, err := readBody(response.Body)
	if err != nil {
		return upnpDescription{}, nil, fmt.Errorf("read the UPnP description of %s: %w", u.host, err)
	}
	var description upnpDescription
	if err := xml.Unmarshal(data, &description); err != nil {
		return upnpDescription{}, nil, fmt.Errorf("read the UPnP description of %s: %w", u.host, err)
	}
	base, err := url.Parse(location)
	if err != nil {
		return upnpDescription{}, nil, err
	}
	if description.URLBase != "" {
		if _, err := sameHost(nil, description.URLBase, routerIP); err == nil {
			base, _ = url.Parse(description.URLBase)
		}
	}
	return description, base, nil
}

// sameHost resolves reference against base and returns it, when it is an
// http URL of the router itself, by its IP address: a router's description
// that points elsewhere is not followed.
func sameHost(base *url.URL, reference string, routerIP netip.Addr) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return "", err
	}
	if base != nil {
		parsed = base.ResolveReference(parsed)
	}
	ip, err := netip.ParseAddr(parsed.Hostname())
	if parsed.Scheme != "http" || err != nil || ip.Unmap() != routerIP || parsed.User != nil {
		return "", fmt.Errorf("%q is not on the router %s", reference, routerIP)
	}
	return parsed.String(), nil
}

// call calls a UPnP action that only reads, and returns the values in its
// answer by name, such as NewTotalBytesReceived.
func (u *UPnPReader) call(ctx context.Context, service upnpService, action string) (map[string]string, error) {
	request, err := soapRequest(ctx, service, action)
	if err != nil {
		return nil, err
	}
	response, err := u.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("ask %s for %s: %w", u.host, action, err)
	}
	defer func() { _ = response.Body.Close() }()
	return soapAnswer(response, u.host, action)
}

// soapRequest returns the request that calls a UPnP or TR-064 action
// without arguments.
func soapRequest(ctx context.Context, service upnpService, action string) (*http.Request, error) {
	body := `<?xml version="1.0"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body><u:` + action + ` xmlns:u="` + xmlEscape(service.ServiceType) + `"></u:` + action + `></s:Body></s:Envelope>`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, service.ControlURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	request.Header.Set("SOAPAction", `"`+service.ServiceType+"#"+action+`"`)
	return request, nil
}

// soapAnswer reads the values of the answer to an action.
func soapAnswer(response *http.Response, host, action string) (map[string]string, error) {
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ask %s for %s: answered %s", host, action, response.Status)
	}
	data, err := readBody(response.Body)
	if err != nil {
		return nil, fmt.Errorf("ask %s for %s: %w", host, action, err)
	}
	values, err := soapValues(data)
	if err != nil {
		return nil, fmt.Errorf("ask %s for %s: %w", host, action, err)
	}
	return values, nil
}

// soapValues returns the text of the elements in a SOAP answer whose names
// start with "New", as UPnP names the values it answers with.
func soapValues(data []byte) (map[string]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	values := map[string]string{}
	var current string
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return values, nil
			}
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			current = t.Name.Local
			text.Reset()
		case xml.CharData:
			if strings.HasPrefix(current, "New") && text.Len() < 256 {
				text.Write(t)
			}
		case xml.EndElement:
			if t.Name.Local == current && strings.HasPrefix(current, "New") {
				values[current] = strings.TrimSpace(text.String())
			}
			current = ""
		}
	}
}

// xmlEscape escapes text for an XML attribute.
func xmlEscape(text string) string {
	var escaped strings.Builder
	_ = xml.EscapeText(&escaped, []byte(text))
	return escaped.String()
}
