// Command usage-control-tray shows an icon in the Windows taskbar that tells
// whether Usage Control runs: the mascot with a green dot while the service
// answers, a red one when it is stopped or does not answer, and a grey one
// while it is paused or starting. Its menu pauses and resumes the service,
// stops it and closes the icon, and opens the page of the hub that collects
// from this PC, or this PC's own page when it shows one.
//
// The installer starts it when someone logs in. The icons are drawn from the
// website's mascot and theme by cmd/tray-icons before it is built.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// serviceState is what Windows tells about the service.
type serviceState int

const (
	serviceStopped serviceState = iota
	serviceStarting
	serviceRunning
	serviceStopping
	serviceMissing
)

// state is what the icon shows.
type state int

const (
	stateRunning state = iota
	stateStarting
	stateStopping
	stateNotAnswering
	stateStopped
	statePaused
	stateMissing
)

// stateOf combines the service's state, whether it answers and whether it
// was paused from the icon.
func stateOf(service serviceState, answers, paused bool) state {
	switch service {
	case serviceRunning:
		if answers {
			return stateRunning
		}
		return stateNotAnswering
	case serviceStarting:
		return stateStarting
	case serviceStopping:
		return stateStopping
	case serviceMissing:
		return stateMissing
	}
	if paused {
		return statePaused
	}
	return stateStopped
}

// stillPaused tells whether a pause from the icon still holds: until the
// service starts again, from the icon or some other way. A service that is
// still stopping after Pause stays paused.
func stillPaused(service serviceState, paused bool) bool {
	return paused && service != serviceRunning && service != serviceStarting
}

// icon is the file in icons/ that shows the state.
func (s state) icon() string {
	switch s {
	case stateRunning:
		return "running.ico"
	case stateStarting, stateStopping, statePaused:
		return "paused.ico"
	}
	return "stopped.ico"
}

// running tells whether the service runs or is about to, so the menu offers
// to pause it rather than to start it.
func (s state) running() bool {
	return s == stateRunning || s == stateStarting || s == stateNotAnswering
}

// settings are the installer's options the icon needs, from the registry.
type settings struct {
	port    string
	website bool
}

// defaultPort is the installer's default for PORT.
const defaultPort = "9393"

// newSettings reads the remembered PORT and WEBSITE options, keeping the
// installer's defaults for missing or invalid ones.
func newSettings(port, website string) settings {
	s := settings{port: defaultPort, website: website == "1"}
	if number, err := strconv.Atoi(port); err == nil && number >= 1 && number <= 65535 {
		s.port = strconv.Itoa(number)
	}
	return s
}

// pageURL is this PC's own page, when it shows one.
func (s settings) pageURL() string {
	return "http://localhost:" + s.port + "/"
}

// addressEvery is how often the PC's address is looked up again. Listing the
// network adapters can take a moment, and the address seldom changes.
const addressEvery = time.Minute

// cachedAddress remembers what lookup found for addressEvery.
type cachedAddress struct {
	lookup func() (netip.Addr, bool)
	at     time.Time
	addr   netip.Addr
	found  bool
}

// get returns the address, looking it up again when it is addressEvery old.
func (c *cachedAddress) get(now time.Time) (netip.Addr, bool) {
	if c.at.IsZero() || now.Sub(c.at) >= addressEvery {
		c.addr, c.found = c.lookup()
		c.at = now
	}
	return c.addr, c.found
}

// localAddress is the address other devices on the network reach this PC
// at: the IPv4 address Windows sends from on its default route, else the
// first private IPv4 address of a network adapter that is up. A PC can have
// many adapters (VPNs, virtual switches), so the routed one comes first.
func localAddress() (netip.Addr, bool) {
	var routed netip.Addr
	// Connecting a UDP socket only picks the route; nothing is sent.
	if conn, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		if local, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			routed, _ = netip.AddrFromSlice(local.IP)
		}
		_ = conn.Close()
	}
	var candidates []netip.Addr
	if interfaces, err := net.Interfaces(); err == nil {
		for _, adapter := range interfaces {
			if adapter.Flags&net.FlagUp == 0 || adapter.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := adapter.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if prefix, err := netip.ParsePrefix(addr.String()); err == nil {
					candidates = append(candidates, prefix.Addr())
				}
			}
		}
	}
	return pickAddress(routed, candidates)
}

// pickAddress prefers the routed address when it is a private IPv4 one, else
// the first private IPv4 address among the candidates.
func pickAddress(routed netip.Addr, candidates []netip.Addr) (netip.Addr, bool) {
	for _, addr := range append([]netip.Addr{routed}, candidates...) {
		if addr = addr.Unmap(); addr.Is4() && addr.IsPrivate() {
			return addr, true
		}
	}
	return netip.Addr{}, false
}

// addressText is the menu line that tells where this PC is reachable.
func (t texts) addressText(addr netip.Addr, found bool, port string) string {
	if !found {
		return t.address + ": " + t.noNetwork
	}
	return t.address + ": " + net.JoinHostPort(addr.String(), port)
}

// hubText is the menu line that opens the hub's page at link. It names the
// hub's address, since any device on the network can claim to be the hub, so
// the address is seen before the page is opened.
func (t texts) hubText(link string) string {
	if u, err := url.Parse(link); err == nil && u.Host != "" {
		return t.openHub + " (" + u.Host + ")"
	}
	return t.openHub
}

// askHub asks the service on this PC where the page of the hub that collects
// from it is. An empty link means no hub has asked yet; an error means the
// service does not answer.
func askHub(ctx context.Context, client *http.Client, port string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/api/hub", http.NoBody)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET /api/hub answered %s", response.Status)
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return "", err
	}
	// The link is opened in the browser, so only a plain http address is
	// taken.
	if link, err := url.Parse(body.URL); body.URL != "" && (err != nil || link.Scheme != "http" || link.Host == "" || link.User != nil) {
		return "", fmt.Errorf("GET /api/hub answered %q, which is not an http address", body.URL)
	}
	return body.URL, nil
}

// texts are the menu's words in one language.
type texts struct {
	running, starting, stopping, notAnswering, stopped, paused, missing string

	// address names the line with this PC's address and port; noNetwork
	// stands for the address when the PC has none on a local network.
	address, noNetwork string

	openHub, noHub, openPage, pause, resume, start, stopAndExit string
	// startFailed and stopFailed take the error.
	startFailed, stopFailed string
}

// stateText is how the menu and the tooltip name a state.
func (t texts) stateText(s state) string {
	return [...]string{
		stateRunning:      t.running,
		stateStarting:     t.starting,
		stateStopping:     t.stopping,
		stateNotAnswering: t.notAnswering,
		stateStopped:      t.stopped,
		statePaused:       t.paused,
		stateMissing:      t.missing,
	}[s]
}

// translations has the languages of the website.
var translations = map[string]texts{
	"en": {
		address: "Address", noNetwork: "no local network",
		running: "Running", starting: "Starting", stopping: "Stopping",
		notAnswering: "Running, but not answering", stopped: "Stopped", paused: "Paused", missing: "Not installed",
		openHub: "Open hub", noHub: "Open hub (no hub has asked yet)", openPage: "Open the page on this PC",
		pause: "Pause", resume: "Resume", start: "Start", stopAndExit: "Stop and exit",
		startFailed: "Could not start Usage Control: %v", stopFailed: "Could not stop Usage Control: %v",
	},
	"de": {
		address: "Adresse", noNetwork: "kein lokales Netzwerk",
		running: "Läuft", starting: "Startet", stopping: "Wird gestoppt",
		notAnswering: "Läuft, antwortet aber nicht", stopped: "Gestoppt", paused: "Pausiert", missing: "Nicht installiert",
		openHub: "Hub öffnen", noHub: "Hub öffnen (noch hat kein Hub gefragt)", openPage: "Seite auf diesem PC öffnen",
		pause: "Pausieren", resume: "Fortsetzen", start: "Starten", stopAndExit: "Stoppen und beenden",
		startFailed: "Usage Control konnte nicht gestartet werden: %v", stopFailed: "Usage Control konnte nicht gestoppt werden: %v",
	},
	"fr": {
		address: "Adresse", noNetwork: "aucun réseau local",
		running: "En marche", starting: "Lancement", stopping: "Arrêt en cours",
		notAnswering: "En marche, mais ne répond pas", stopped: "Arrêté", paused: "En pause", missing: "Non installé",
		openHub: "Ouvrir le hub", noHub: "Ouvrir le hub (aucun hub n'a encore demandé)", openPage: "Ouvrir la page sur ce PC",
		pause: "Mettre en pause", resume: "Reprendre", start: "Démarrer", stopAndExit: "Arrêter et quitter",
		startFailed: "Impossible de démarrer Usage Control : %v", stopFailed: "Impossible d'arrêter Usage Control : %v",
	},
	"es": {
		address: "Dirección", noNetwork: "sin red local",
		running: "En marcha", starting: "Iniciando", stopping: "Deteniendo",
		notAnswering: "En marcha, pero no responde", stopped: "Detenido", paused: "En pausa", missing: "No instalado",
		openHub: "Abrir el hub", noHub: "Abrir el hub (ningún hub ha preguntado aún)", openPage: "Abrir la página en este PC",
		pause: "Pausar", resume: "Reanudar", start: "Iniciar", stopAndExit: "Detener y salir",
		startFailed: "No se pudo iniciar Usage Control: %v", stopFailed: "No se pudo detener Usage Control: %v",
	},
}

// textsFor picks the first of the user's languages, such as "de-DE", that
// the menu has, or English.
func textsFor(languages []string) texts {
	for _, language := range languages {
		code, _, _ := strings.Cut(strings.ToLower(language), "-")
		if t, ok := translations[code]; ok {
			return t
		}
	}
	return translations["en"]
}
