package router

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// ASUSPaths are the only pages of an ASUS router's web interface a reader
// asks: the login, the values the ASUS app reads, and the temperatures.
// None of them changes a setting.
var ASUSPaths = []string{"/login.cgi", "/appGet.cgi", "/ajax_coretmp.asp"}

const (
	// asusUserAgent is the user agent of the ASUS app, the only one an ASUS
	// router gives a login token to instead of its login page.
	asusUserAgent = "asusrouter--DUTUtil-"
	// asusHooks are the values asked from appGet.cgi.
	asusHooks = "cpu_usage(appobj);memory_usage(appobj);netdev(appobj);uptime();get_clientlist()"
	// asusLoginBackoff is how long a refused login is not tried again, so a
	// wrong password does not lock the router's admin out.
	asusLoginBackoff = 5 * time.Minute
)

// errASUSLogin is a login the router refused.
var errASUSLogin = errors.New("the router refused the login; check the user and password")

// ASUSReader reads an ASUS router through its web interface, as the ASUS
// app does: CPU, memory, traffic per port and Wi-Fi band, clients and
// temperatures. It logs in once and keeps the token; some models sign the
// admin out of the web interface then.
type ASUSReader struct {
	base     string
	user     string
	password string
	client   *http.Client

	mu          sync.Mutex
	token       string
	refusedAt   time.Time
	refusedLogs bool
	cores       map[string][2]uint64 // usage and total of each core, from before
	traffic     map[string]*[2]counter
	uptime      uint64
	uptimeRead  bool
}

// NewASUS returns a reader for the ASUS router at address (host or
// host:port) with the login of its web interface.
func NewASUS(address, user, password string) *ASUSReader {
	return &ASUSReader{base: "http://" + address, user: user, password: password, client: newClient(localNetworkOnly)}
}

// Collect reads the router's usage, logging in first when there is no
// token yet or the router no longer takes it.
func (a *ASUSReader) Collect(ctx context.Context) (metrics.Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	values, err := a.values(ctx)
	if err != nil {
		return metrics.Snapshot{}, err
	}
	snapshot, err := a.snapshot(values, time.Now())
	if err != nil {
		return metrics.Snapshot{}, err
	}
	snapshot.Temperatures = a.temperatures(ctx)
	return snapshot, nil
}

// values asks appGet.cgi, and logs in again once when the token is no
// longer taken.
func (a *ASUSReader) values(ctx context.Context) (map[string]json.RawMessage, error) {
	for attempt := 0; ; attempt++ {
		if a.token == "" {
			if err := a.login(ctx); err != nil {
				return nil, err
			}
		}
		values, err := a.appGet(ctx)
		if err == nil {
			return values, nil
		}
		a.token = ""
		if !errors.Is(err, errASUSSignedOut) || attempt > 0 {
			return nil, err
		}
	}
}

// login asks the router for a token. A refused login is not tried again for
// asusLoginBackoff.
func (a *ASUSReader) login(ctx context.Context) error {
	if !a.refusedAt.IsZero() && time.Since(a.refusedAt) < asusLoginBackoff {
		return errASUSLogin
	}
	form := url.Values{"login_authorization": {base64.StdEncoding.EncodeToString([]byte(a.user + ":" + a.password))}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+"/login.cgi", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", asusUserAgent)
	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("log in to %s: %w", a.base, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := readBody(response.Body)
	if err != nil {
		return fmt.Errorf("log in to %s: %w", a.base, err)
	}
	var answer struct {
		Token       string `json:"asus_token"`
		ErrorStatus any    `json:"error_status"`
	}
	if json.Unmarshal(data, &answer) != nil {
		// Not an answer to the login, as from a busy router: tried again at
		// the next reading.
		return fmt.Errorf("log in to %s: answered %s without a login answer", a.base, response.Status)
	}
	if answer.Token == "" {
		a.refusedAt = time.Now()
		if !a.refusedLogs {
			// Only once, so the log does not fill while the login is wrong.
			slog.Warn("an ASUS router refused the login; it is tried again every few minutes", "router", a.base, "status", response.Status, "error_status", answer.ErrorStatus)
			a.refusedLogs = true
		}
		return errASUSLogin
	}
	a.token, a.refusedAt, a.refusedLogs = answer.Token, time.Time{}, false
	return nil
}

// errASUSSignedOut is an answer that shows the token is no longer taken.
var errASUSSignedOut = errors.New("the router no longer takes the login token")

// trailingComma matches a comma before a closing brace, which some ASUS
// firmware writes into its JSON.
var trailingComma = regexp.MustCompile(`,\s*([}\]])`)

// appGet asks appGet.cgi for the values in asusHooks.
func (a *ASUSReader) appGet(ctx context.Context) (map[string]json.RawMessage, error) {
	data, status, err := a.ask(ctx, http.MethodPost, "/appGet.cgi", url.Values{"hook": {asusHooks}})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("%w: answered %d", errASUSSignedOut, status)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(trailingComma.ReplaceAll(data, []byte("$1")), &values); err != nil {
		// The login page comes back when the token is no longer taken.
		return nil, fmt.Errorf("%w: %w", errASUSSignedOut, err)
	}
	if _, ok := values["error_status"]; ok {
		return nil, errASUSSignedOut
	}
	return values, nil
}

// ask asks a page of the web interface with the token.
func (a *ASUSReader) ask(ctx context.Context, method, path string, form url.Values) ([]byte, int, error) {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	request, err := http.NewRequestWithContext(ctx, method, a.base+path, body)
	if err != nil {
		return nil, 0, err
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	request.Header.Set("User-Agent", asusUserAgent)
	request.Header.Set("Cookie", "asus_token="+a.token)
	response, err := a.client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("ask %s%s: %w", a.base, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := readBody(response.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("ask %s%s: %w", a.base, path, err)
	}
	return data, response.StatusCode, nil
}

// uptimePattern finds the seconds since boot in the uptime, such as
// "Thu, 10 Oct 2026 08:00:00 +0200(12345 secs since boot)".
var uptimePattern = regexp.MustCompile(`\((\d+) secs since boot\)`)

// snapshot turns the values of appGet.cgi into a snapshot, with CPU usage
// and traffic measured since the values from before.
func (a *ASUSReader) snapshot(values map[string]json.RawMessage, now time.Time) (metrics.Snapshot, error) {
	var snapshot metrics.Snapshot
	var uptimeText string
	if json.Unmarshal(values["uptime"], &uptimeText) == nil {
		if match := uptimePattern.FindStringSubmatch(uptimeText); match != nil {
			snapshot.UptimeSeconds, _ = strconv.ParseUint(match[1], 10, 64)
		}
	}
	restarted := a.uptimeRead && snapshot.UptimeSeconds < a.uptime
	a.uptime, a.uptimeRead = snapshot.UptimeSeconds, snapshot.UptimeSeconds > 0
	if restarted {
		a.cores = nil
	}

	var memory map[string]string
	if json.Unmarshal(values["memory_usage"], &memory) == nil {
		total, totalErr := strconv.ParseUint(memory["mem_total"], 10, 64)
		used, usedErr := strconv.ParseUint(memory["mem_used"], 10, 64)
		if totalErr == nil && usedErr == nil && total > 0 && used <= total {
			snapshot.Memory = metrics.Memory{TotalBytes: total << 10, UsedBytes: used << 10, UsedPercent: float64(used) / float64(total) * 100}
			if free, err := strconv.ParseUint(memory["mem_free"], 10, 64); err == nil && free <= total {
				snapshot.Memory.AvailableBytes = free << 10
			}
		}
	}

	var cpu map[string]string
	if json.Unmarshal(values["cpu_usage"], &cpu) == nil {
		snapshot.CPU = a.cpu(cpu)
	}

	var netdev map[string]string
	if json.Unmarshal(values["netdev"], &netdev) == nil {
		snapshot.Network = a.network(netdev, now, restarted)
	}

	if snapshot.Memory.TotalBytes == 0 && snapshot.CPU.Cores == 0 && len(snapshot.Network) == 0 {
		return metrics.Snapshot{}, fmt.Errorf("the router %s answered without CPU, memory or traffic", a.base)
	}

	var clients map[string]json.RawMessage
	if json.Unmarshal(values["get_clientlist"], &clients) == nil && clients != nil {
		snapshot.Extras = append(snapshot.Extras, clientExtras(clients))
	}
	return snapshot, nil
}

// cpu measures the usage of each core since the values from before, or
// since the router started for the first reading.
func (a *ASUSReader) cpu(values map[string]string) metrics.CPU {
	previous := a.cores
	a.cores = map[string][2]uint64{}
	var cores []float64
	reset := false
	for n := 1; ; n++ {
		core := "cpu" + strconv.Itoa(n)
		total, totalErr := strconv.ParseUint(values[core+"_total"], 10, 64)
		usage, usageErr := strconv.ParseUint(values[core+"_usage"], 10, 64)
		if totalErr != nil || usageErr != nil {
			break
		}
		a.cores[core] = [2]uint64{usage, total}
		before := previous[core]
		if before[1] > 0 {
			if total < before[1] || usage < before[0] {
				// The counters started again, as after a restart: this
				// reading has no usage to tell.
				reset = true
				continue
			}
			usage, total = usage-before[0], total-before[1]
		}
		percent := 0.0
		if total > 0 {
			percent = min(100, float64(usage)/float64(total)*100)
		}
		cores = append(cores, percent)
	}
	if len(cores) == 0 || reset {
		return metrics.CPU{}
	}
	var sum float64
	for _, core := range cores {
		sum += core
	}
	return metrics.CPU{UsagePercent: sum / float64(len(cores)), Cores: len(cores), CoreUsagePercent: cores}
}

// network turns the byte counters of netdev, such as INTERNET_rx, into the
// traffic of each port and Wi-Fi band.
func (a *ASUSReader) network(values map[string]string, now time.Time, restarted bool) []metrics.NetworkInterface {
	if a.traffic == nil {
		a.traffic = map[string]*[2]counter{}
	}
	names := map[string]bool{}
	for key := range values {
		if name, ok := strings.CutSuffix(key, "_rx"); ok {
			names[name] = true
		}
	}
	var interfaces []metrics.NetworkInterface
	for _, name := range slices.Sorted(maps.Keys(names)) {
		shown := asusInterfaceName(name)
		if shown == "" {
			continue
		}
		received, errReceived := parseHex(values[name+"_rx"])
		sent, errSent := parseHex(values[name+"_tx"])
		if errReceived != nil || errSent != nil {
			continue
		}
		counters := a.traffic[name]
		if counters == nil {
			counters = &[2]counter{}
			a.traffic[name] = counters
		}
		network := metrics.NetworkInterface{Name: shown, ReceivedBytes: received, SentBytes: sent}
		network.ReceiveBytesPerSecond, _ = counters[0].rate(received, now, restarted)
		network.SendBytesPerSecond, _ = counters[1].rate(sent, now, restarted)
		interfaces = append(interfaces, network)
	}
	// The internet first, then the cables, then the Wi-Fi bands.
	slices.SortStableFunc(interfaces, func(x, y metrics.NetworkInterface) int {
		return asusOrder(x.Name) - asusOrder(y.Name)
	})
	return interfaces
}

// asusInterfaceName names an interface of netdev as the page shows it, or
// returns empty for one that is left out: the bridge, which only adds up
// the others.
func asusInterfaceName(name string) string {
	switch {
	case name == "BRIDGE":
		return ""
	case name == "INTERNET":
		return "WAN"
	case strings.HasPrefix(name, "INTERNET"):
		return "WAN " + strings.TrimPrefix(name, "INTERNET")
	case name == "WIRED":
		return "LAN"
	case name == "WIRELESS0":
		return "Wi-Fi 2.4 GHz"
	case name == "WIRELESS1":
		return "Wi-Fi 5 GHz"
	case strings.HasPrefix(name, "WIRELESS"):
		number, err := strconv.Atoi(strings.TrimPrefix(name, "WIRELESS"))
		if err != nil {
			return name
		}
		return "Wi-Fi " + strconv.Itoa(number+1)
	}
	return name
}

func asusOrder(name string) int {
	switch {
	case strings.HasPrefix(name, "WAN"):
		return 0
	case name == "LAN":
		return 1
	case strings.HasPrefix(name, "Wi-Fi"):
		return 2
	}
	return 3
}

// parseHex reads a counter written as hex, such as "0x1a2b".
func parseHex(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if digits, ok := strings.CutPrefix(strings.ToLower(value), "0x"); ok {
		return strconv.ParseUint(digits, 16, 64)
	}
	return strconv.ParseUint(value, 10, 64)
}

// clientExtras counts the clients of the router that are online, on a
// cable and on Wi-Fi, from get_clientlist.
func clientExtras(list map[string]json.RawMessage) metrics.Extra {
	var wired, wireless float64
	for key, raw := range list {
		if key == "maclist" || key == "ClientAPILevel" {
			continue
		}
		var client struct {
			Online string `json:"isOnline"`
			WL     string `json:"isWL"`
		}
		if json.Unmarshal(raw, &client) != nil || client.Online != "1" {
			continue
		}
		if client.WL == "" || client.WL == "0" {
			wired++
		} else {
			wireless++
		}
	}
	total := wired + wireless
	return metrics.Extra{
		ID: "clients", Title: "Clients", Titles: map[string]string{"de": "Geräte im Netz", "fr": "Appareils connectés", "es": "Dispositivos conectados"},
		Items: []metrics.ExtraItem{
			{ID: "online", Label: "Online", Unit: metrics.UnitNumber, Value: &total, History: true, Labels: map[string]string{"de": "Online", "fr": "En ligne", "es": "En línea"}},
			{ID: "wired", Label: "On a cable", Unit: metrics.UnitNumber, Value: &wired, Labels: map[string]string{"de": "Per Kabel", "fr": "Par câble", "es": "Por cable"}},
			{ID: "wireless", Label: "On Wi-Fi", Unit: metrics.UnitNumber, Value: &wireless, Labels: map[string]string{"de": "Per WLAN", "fr": "En Wi-Fi", "es": "Por Wi-Fi"}},
		},
	}
}

// temperaturePattern finds the temperatures in ajax_coretmp.asp, such as
// curr_coreTmp_2_raw = "48°C" for the 2.4 GHz radio, or curr_cpuTemp =
// "62" for the CPU.
var temperaturePattern = regexp.MustCompile(`curr_(coreTmp_(\w+?)_raw|cpuTemp)\s*=\s*"?\s*([0-9]+(?:\.[0-9]+)?)`)

// temperatures reads the temperatures of the radios and the CPU, where the
// model reports them; none when it does not.
func (a *ASUSReader) temperatures(ctx context.Context) []metrics.Temperature {
	data, status, err := a.ask(ctx, http.MethodGet, "/ajax_coretmp.asp", nil)
	if err != nil || status != http.StatusOK {
		return nil
	}
	var temperatures []metrics.Temperature
	for _, match := range temperaturePattern.FindAllStringSubmatch(string(data), -1) {
		celsius, err := strconv.ParseFloat(match[3], 64)
		if err != nil || celsius <= 0 || celsius > 150 {
			continue
		}
		sensor := "CPU"
		switch match[2] {
		case "":
		case "2":
			sensor = "Wi-Fi 2.4 GHz"
		case "5":
			sensor = "Wi-Fi 5 GHz"
		case "52":
			sensor = "Wi-Fi 5 GHz (2)"
		case "6":
			sensor = "Wi-Fi 6 GHz"
		default:
			sensor = "Wi-Fi " + match[2]
		}
		temperatures = append(temperatures, metrics.Temperature{Sensor: sensor, Celsius: celsius})
	}
	return temperatures
}
