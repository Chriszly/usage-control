package server

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"

	"github.com/Chriszly/usage-control/backend/internal/hub"
)

// hubLink remembers where the page of the hub that collects from this machine
// is: the address its requests come from, with the port it sends in the
// hub.PagePortHeader header. The tray icon on Windows asks for it, to open
// the hub's page.
type hubLink struct {
	mu  sync.Mutex
	url string
}

// remember notes the hub's page from a request that carries the header, then
// passes the request on.
func (l *hubLink) remember(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if url, ok := hubURL(r); ok {
			l.mu.Lock()
			l.url = url
			l.mu.Unlock()
		}
		next(w, r)
	}
}

// hubURL builds the hub's page address from a request, if it is from a hub.
func hubURL(r *http.Request) (string, bool) {
	value := r.Header.Get(hub.PagePortHeader)
	if value == "" {
		return "", false
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return "", false
	}
	sender, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return "", false
	}
	host := sender.Addr().Unmap().WithZone("").String()
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/", true
}

// hubLinkResponse is the body of GET /api/hub.
type hubLinkResponse struct {
	// URL is the hub's page, or empty when no hub has asked yet.
	URL string `json:"url"`
}

// handler serves GET /api/hub, only to programs on this machine: the tray
// icon, not the network.
func (l *hubLink) handler(w http.ResponseWriter, r *http.Request) {
	sender, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !sender.Addr().IsLoopback() {
		http.Error(w, "only answered on this machine", http.StatusForbidden)
		return
	}
	l.mu.Lock()
	response := hubLinkResponse{URL: l.url}
	l.mu.Unlock()
	writeJSON(w, http.StatusOK, response)
}
