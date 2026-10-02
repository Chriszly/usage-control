// Package server serves the metrics API and the website.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// Collector reads the current usage of the machine.
type Collector interface {
	Collect(ctx context.Context) (metrics.Snapshot, error)
}

// New returns the handler for the whole site: the JSON API under /api/ and
// the website from site. Requests from outside the local network are refused.
func New(collector Collector, h History, site fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/metrics", metricsHandler(collector))
	mux.HandleFunc("GET /api/history", historyHandler(h))
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

// websiteHandler serves the website, which is built once per language into
// its own folder, such as de/ for German. Every language is served at the same
// address: each request is answered from the folder of the visitor's language,
// so switching language never changes the page's address.
func websiteHandler(site fs.FS) http.Handler {
	available := languages(site)
	if len(available) == 0 {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "The website is not built. Run `npm run build` in frontend/ and build the backend again.", http.StatusNotFound)
		})
	}
	byLanguage := make(map[string]http.Handler, len(available))
	for _, language := range available {
		folder, err := fs.Sub(site, language)
		if err != nil {
			// fs.Sub only fails for an invalid path, and languages only returns folder names.
			panic(err)
		}
		byLanguage[language] = http.FileServerFS(folder)
	}
	shared := http.FileServerFS(site)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The same address holds a different file per language, so browsers and
		// caches must check again instead of reusing a file from another language.
		w.Header().Set("Vary", "Accept-Language, Cookie")
		w.Header().Set("Cache-Control", "no-cache")

		language := pickLanguage(r, available)
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" || fileExists(site, language+"/"+name) {
			byLanguage[language].ServeHTTP(w, r)
			return
		}
		// Files built once for all languages, such as 3rdpartylicenses.txt.
		shared.ServeHTTP(w, r)
	})
}

func fileExists(site fs.FS, name string) bool {
	if !fs.ValidPath(name) {
		return false
	}
	info, err := fs.Stat(site, name)
	return err == nil && !info.IsDir()
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
