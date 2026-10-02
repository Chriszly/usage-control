// Package server serves the metrics API and the website.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// Collector reads the current usage of the machine.
type Collector interface {
	Collect(ctx context.Context) (metrics.Snapshot, error)
}

// New returns the handler for the whole site: the JSON API under /api/ and
// the website from site. Requests from outside the local network are refused.
func New(collector Collector, site fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/metrics", metricsHandler(collector))
	mux.Handle("GET /", websiteHandler(site))
	return localNetworkOnly(mux)
}

func metricsHandler(collector Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := collector.Collect(r.Context())
		if err != nil {
			slog.Error("collect metrics", "error", err)
			http.Error(w, "could not read the machine's usage", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(snapshot); err != nil {
			slog.Error("write metrics response", "error", err)
		}
	}
}

func websiteHandler(site fs.FS) http.Handler {
	if _, err := fs.Stat(site, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "The website is not built. Run `npm run build` in frontend/ and build the backend again.", http.StatusNotFound)
		})
	}
	return http.FileServerFS(site)
}

// localNetworkOnly refuses requests whose sender is not on the local network:
// loopback, private (RFC 1918 and IPv6 ULA) and link-local addresses. It reads
// the address of the TCP connection, not a header a client could fake.
func localNetworkOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalNetwork(r.RemoteAddr) {
			http.Error(w, "only reachable from the local network", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalNetwork(remoteAddr string) bool {
	addrPort, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return false
	}
	addr := addrPort.Addr().Unmap()
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast()
}
