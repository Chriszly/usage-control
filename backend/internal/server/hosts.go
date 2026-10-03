package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strings"
)

// knownHosts are the names a request may address this machine by. Checking
// them stops DNS rebinding: a web page from the internet whose domain is
// pointed at this machine's LAN address would reach the API from a LAN
// browser, past the address check, but its requests carry that domain as the
// Host header, which is not one of these names.
type knownHosts struct {
	// hostname is the machine's own name, in lower case.
	hostname string
	// allowed are the names from ALLOWED_HOSTS, in lower case.
	allowed []string
}

// newKnownHosts returns the names of this machine plus the allowed ones.
func newKnownHosts(allowed []string) knownHosts {
	hostname, _ := os.Hostname()
	k := knownHosts{hostname: strings.ToLower(hostname)}
	for _, name := range allowed {
		if name = normalizeHost(name); name != "" {
			k.allowed = append(k.allowed, name)
		}
	}
	return k
}

// allows reports whether a request with this Host header may be answered: an
// IP address, localhost, the machine's own hostname, a .local name (mDNS
// names cannot be pointed anywhere through public DNS) or an allowed name. An
// empty header, which only an HTTP/1.0 client sends, is let through: no
// browser sends one.
func (k knownHosts) allows(hostHeader string) bool {
	host := normalizeHost(hostHeader)
	if host == "" {
		return hostHeader == ""
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	if host == "localhost" || host == k.hostname {
		return true
	}
	if name, isLocal := strings.CutSuffix(host, ".local"); isLocal && name != "" {
		return true
	}
	return slices.Contains(k.allowed, host)
}

// normalizeHost returns the name or address in a Host header or an
// ALLOWED_HOSTS entry: without the port and brackets, in lower case and
// without a trailing dot.
func normalizeHost(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	return strings.ToLower(strings.TrimSuffix(value, "."))
}

// knownHostsOnly refuses requests that address this machine by a name it does
// not know, with 421 Misdirected Request, before any handler runs.
func knownHostsOnly(hosts knownHosts, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts.allows(r.Host) {
			http.Error(w, fmt.Sprintf("this device does not answer to the name %q; open the page by IP address or by the device's own name, or list the name in ALLOWED_HOSTS", normalizeHost(r.Host)),
				http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}
