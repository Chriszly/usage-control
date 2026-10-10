// Package router reads the usage of routers, which cannot run usage-control,
// in a language they already speak, so a hub can show them as devices of
// their own: UPnP, which most home routers answer without a login, SNMP,
// TR-064 of a FRITZ!Box and the web interface of ASUS routers.
//
// It only reads: no request it sends changes a setting of the router. It
// only connects to the local network, and nothing it reads leaves it.
package router

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/lan"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// Protocol is how a router is read.
type Protocol string

// The protocols a router can be read with.
const (
	// UPnP reads the internet traffic and connection without a login, from
	// routers with UPnP switched on, which most home routers have: ASUS,
	// Technicolor, FRITZ!Box and most boxes of internet providers.
	UPnP Protocol = "upnp"
	// ASUS reads CPU, memory, traffic per port and Wi-Fi band, clients and
	// temperatures from the web interface of an ASUS router, with a login.
	ASUS Protocol = "asus"
	// SNMP reads the traffic of every port and, where the router has them,
	// CPU and memory over SNMPv2c, with a community (default public).
	SNMP Protocol = "snmp"
	// SNMPv3 reads the same over SNMPv3, with a user whose password signs
	// and encrypts each request.
	SNMPv3 Protocol = "snmpv3"
	// FritzBox reads the internet traffic and line, the uptime and the
	// clients of a FRITZ!Box over TR-064, with the login of a FRITZ!Box user.
	FritzBox Protocol = "fritzbox"
)

// protocols are the protocols HUB_ROUTERS takes.
var protocols = []Protocol{UPnP, ASUS, SNMP, SNMPv3, FritzBox}

// Config is a router the hub reads, from HUB_ROUTERS.
type Config struct {
	Name     string
	Protocol Protocol
	// Address is the router's host name or IP address, with a port where
	// the router does not answer on the usual one (not for UPnP).
	Address string
	// User is the login, for the protocols that need one.
	User string
}

// NeedsLogin reports whether the protocol needs a user and password.
func (p Protocol) NeedsLogin() bool {
	return p == ASUS || p == SNMPv3 || p == FritzBox
}

// HasSecret reports whether the protocol reads a secret from the passwords
// file: the password of a login, or the community of SNMPv2c, which is
// public when the file has none.
func (p Protocol) HasSecret() bool {
	return p.NeedsLogin() || p == SNMP
}

// hostPattern matches a host name or IPv4 address, or an IPv6 address in
// brackets, with an optional port: the address becomes part of the URLs
// that are asked, so no path, user or other URL parts.
var hostPattern = regexp.MustCompile(`^(?:[A-Za-z0-9.-]+|\[[0-9A-Fa-f:.]+\])(?::[0-9]{1,5})?$`)

// userPattern matches a user name of a router's web interface.
var userPattern = regexp.MustCompile(`^[^\s@=,:]{1,64}$`)

// ParseConfigs reads the routers from a comma-separated list of entries such
// as "Router=upnp:192.168.1.1", "Router=asus:admin@192.168.1.1",
// "Router=snmp:192.168.1.1", "Router=snmpv3:monitor@192.168.1.1" or
// "Router=fritzbox:monitor@192.168.178.1".
func ParseConfigs(value string) ([]Config, error) {
	var configs []Config
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		config, err := parseConfig(entry)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", entry, err)
		}
		configs = append(configs, config)
	}
	return configs, nil
}

func parseConfig(entry string) (Config, error) {
	name, rest, named := strings.Cut(entry, "=")
	if !named || strings.TrimSpace(name) == "" {
		return Config{}, errors.New(`write each router as name=protocol:address, such as Router=upnp:192.168.1.1`)
	}
	protocol, address, ok := strings.Cut(strings.TrimSpace(rest), ":")
	if !ok {
		return Config{}, errors.New(`give the protocol before the address, such as upnp:192.168.1.1 or asus:admin@192.168.1.1`)
	}
	config := Config{Name: strings.TrimSpace(name), Protocol: Protocol(strings.ToLower(protocol))}
	if !slices.Contains(protocols, config.Protocol) {
		return Config{}, fmt.Errorf("the protocol must be one of %v", protocols)
	}
	if config.Protocol.NeedsLogin() {
		user, host, ok := strings.Cut(address, "@")
		if !ok || !userPattern.MatchString(user) {
			return Config{}, fmt.Errorf("give the router's user before its address, such as %s:admin@192.168.1.1", config.Protocol)
		}
		config.User, address = user, host
	}
	if !hostPattern.MatchString(address) {
		return Config{}, errors.New("the address must be the router's host name or IP address, such as 192.168.1.1")
	}
	if config.Protocol == UPnP {
		if _, _, err := net.SplitHostPort(address); err == nil {
			return Config{}, errors.New("give the router's address without a port; UPnP finds the port itself")
		}
	}
	config.Address = address
	return config, nil
}

// PasswordsFile is the name of the file the router passwords are read from,
// in the folder systemd hands the service its credentials in (see
// LoadCredentialEncrypted in linux/install.sh), when ROUTER_PASSWORDS_FILE
// does not name another.
const PasswordsFile = "router-passwords"

// PasswordsPath returns where the router passwords are read from:
// ROUTER_PASSWORDS_FILE, or else the file systemd decrypted into
// CREDENTIALS_DIRECTORY, or else empty when there is none.
func PasswordsPath() string {
	if path := strings.TrimSpace(os.Getenv("ROUTER_PASSWORDS_FILE")); path != "" {
		return path
	}
	if dir := os.Getenv("CREDENTIALS_DIRECTORY"); dir != "" {
		return filepath.Join(dir, PasswordsFile)
	}
	return ""
}

// maxPasswordsBytes is the largest passwords file that is read.
const maxPasswordsBytes = 64 << 10

// ReadPasswords reads the passwords of the routers from the file at path,
// with one "router name=password" per line; empty lines and lines starting
// with # are skipped. The names are compared as device ids, so case and
// spaces do not matter.
func ReadPasswords(path string) (map[string]string, error) {
	file, err := os.Open(path) //nolint:gosec // the file the hub's owner set up for this
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	passwords := map[string]string{}
	scanner := bufio.NewScanner(io.LimitReader(file, maxPasswordsBytes))
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(text) == "" || strings.HasPrefix(strings.TrimSpace(text), "#") {
			continue
		}
		name, password, ok := strings.Cut(text, "=")
		if !ok || strings.TrimSpace(name) == "" || password == "" {
			// The line is not shown, as it may hold a password.
			return nil, fmt.Errorf("%s, line %d: write each router as name=password", path, line)
		}
		passwords[strings.TrimSpace(name)] = password
	}
	return passwords, scanner.Err()
}

// Reader reads the usage of one router.
type Reader interface {
	Collect(ctx context.Context) (metrics.Snapshot, error)
}

// New returns the reader for a router; password is the login for a
// protocol that needs one, or the community for SNMPv2c.
func New(config Config, password string) Reader {
	switch config.Protocol {
	case ASUS:
		return everyInterval(NewASUS(config.Address, config.User, password), asusInterval)
	case SNMP:
		if password == "" {
			password = "public"
		}
		return NewSNMP(config.Address, password)
	case SNMPv3:
		return NewSNMPv3(config.Address, config.User, password)
	case FritzBox:
		return everyInterval(NewFritzBox(config.Address, config.User, password), fritzInterval)
	default:
		return NewUPnP(config.Address)
	}
}

const (
	// requestTimeout is how long a router has to answer one request.
	requestTimeout = 4 * time.Second
	// maxResponseBytes is the largest answer of a router that is read.
	maxResponseBytes = 1 << 20
)

// newClient returns an HTTP client that only connects to the local network,
// with control checking each address it connects to.
func newClient(control func(network, address string, c syscall.RawConn) error) *http.Client {
	dialer := &net.Dialer{Timeout: requestTimeout, Control: control}
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			// No proxy: the router is on the local network.
			DialContext:         dialer.DialContext,
			MaxIdleConnsPerHost: 1,
		},
		// A redirect could lead off the local network, or to a page that
		// changes a setting.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// localNetworkOnly refuses to connect to an address outside the local
// network, even when a host name resolves to one.
func localNetworkOnly(_, address string, _ syscall.RawConn) error {
	if !lan.Default.LocalAddrPort(address) {
		return fmt.Errorf("%s is not on the local network", address)
	}
	return nil
}

// readBody reads an answer of at most maxResponseBytes, and fails on one
// that is longer.
func readBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("the answer is larger than the %d KiB that are read", maxResponseBytes>>10)
	}
	return data, nil
}

// asusInterval is how often an ASUS router is read: its web interface runs
// on a weak CPU, which a login keeps busy, so less often than a device.
const asusInterval = 30 * time.Second

// throttled reads a router at most once per interval, and answers with the
// reading from before in between. A failed reading is not kept: the next one
// asks again, so a single lost answer is not an outage of a whole interval.
type throttled struct {
	reader   Reader
	interval time.Duration

	mu     sync.Mutex
	latest metrics.Snapshot
	at     time.Time
}

func everyInterval(reader Reader, interval time.Duration) Reader {
	return &throttled{reader: reader, interval: interval}
}

func (t *throttled) Collect(ctx context.Context) (metrics.Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.at.IsZero() && time.Since(t.at) < t.interval {
		return t.latest, nil
	}
	snapshot, err := t.reader.Collect(ctx)
	if err != nil {
		t.at = time.Time{}
		return metrics.Snapshot{}, err
	}
	t.latest, t.at = snapshot, time.Now()
	return snapshot, nil
}
