// Package server serves the metrics API and the website.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/hub"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
	"github.com/Chriszly/usage-control/backend/internal/update"
)

// Collector reads the current usage of the machine.
type Collector interface {
	Collect(ctx context.Context) (metrics.Snapshot, error)
}

// Device is a machine whose usage the site shows: the one it runs on and, in
// hub mode, the other devices it collects from.
type Device struct {
	// ID picks the device in the API, as ?device=<id>.
	ID string `json:"id"`
	// Name is how the page shows the device. It is empty for the machine the
	// site runs on when no name is set; the page then calls it "Host Hub".
	Name string `json:"name"`
	// Address is where another device is reachable, as host:port; empty for
	// the machine the site runs on.
	Address string `json:"address,omitempty"`
	// Removable is set for a device added on the page, which can be removed
	// there too.
	Removable bool `json:"removable"`
	// Unreachable is set for another device that has not answered recently.
	Unreachable bool `json:"unreachable,omitempty"`
	// UnreachableSince is when it stopped answering, when that is known.
	UnreachableSince *time.Time    `json:"unreachableSince,omitempty"`
	Metrics          Collector     `json:"-"`
	History          HistoryReader `json:"-"`
	// Availability is set for the devices the hub collects from.
	Availability AvailabilityReader `json:"-"`
}

// AvailabilityReader tells how long a device did not answer the hub since it
// was added.
type AvailabilityReader interface {
	Availability(ctx context.Context) (hub.Availability, error)
}

// Devices lists the devices the site shows. The first is the machine the site
// runs on, which the API answers for when a request names no device.
type Devices interface {
	List() []Device
}

// DeviceList is a fixed list of devices.
type DeviceList []Device

// List returns the devices.
func (l DeviceList) List() []Device {
	return l
}

// Site is what the website shows and keeps.
type Site struct {
	Devices Devices
	// Hub adds and removes devices on the page, guarded by Password.
	Hub      Hub
	Password Password
	// Retention is how long the history is kept.
	Retention time.Duration
	// Files is the built website.
	Files fs.FS
	// Update tells the running version and whether a newer release exists;
	// nil leaves out GET /api/update.
	Update func() update.Status
}

// New returns the handler for the whole site: the JSON API under /api/ and
// the website. Requests from outside the local network are refused.
func New(site Site) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/devices", devicesHandler(site))
	if site.Hub != nil && site.Password != nil {
		changes := &deviceChanges{hub: site.Hub, password: site.Password}
		mux.HandleFunc("POST /api/devices", changes.add)
		mux.HandleFunc("DELETE /api/devices/{id}", changes.remove)
	}
	mux.HandleFunc("GET /api/metrics", forDevice(site.Devices, metricsHandler))
	mux.HandleFunc("GET /api/history", forDevice(site.Devices, func(d Device) http.HandlerFunc {
		return historyHandler(d.History, site.Retention)
	}))
	mux.HandleFunc("GET /api/availability", forDevice(site.Devices, availabilityHandler))
	if site.Update != nil {
		mux.HandleFunc("GET /api/update", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, site.Update())
		})
	}
	mux.Handle("GET /", websiteHandler(site.Files))
	return localNetworkOnly(mux)
}

// NewDataOnly returns the handler for a device that a hub collects from
// without a website of its own: only GET /api/metrics, with the usage of the
// machine it runs on. Requests from outside the local network are refused.
func NewDataOnly(collector Collector) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/metrics", metricsHandler(Device{ID: hub.LocalID, Metrics: collector}))
	return localNetworkOnly(mux)
}

// devicesResponse is the body of GET /api/devices.
type devicesResponse struct {
	Devices []Device `json:"devices"`
	// PasswordSet tells whether adding or removing a device asks for the
	// password, or chooses it.
	PasswordSet bool `json:"passwordSet"`
}

// devicesHandler serves GET /api/devices: the devices the site shows.
func devicesHandler(site Site) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := devicesResponse{Devices: site.Devices.List()}
		if site.Password != nil {
			set, err := site.Password.IsSet(r.Context())
			if err != nil {
				slog.Error("read whether the password is set", "error", err)
				http.Error(w, "could not read the settings", http.StatusInternalServerError)
				return
			}
			response.PasswordSet = set
		}
		writeJSON(w, http.StatusOK, response)
	}
}

// forDevice passes a request on to the handler for the device in its
// ?device= parameter, or for the first device when there is none.
func forDevice(devices Devices, handler func(Device) http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list := devices.List()
		id := r.URL.Query().Get("device")
		if id == "" && len(list) > 0 {
			id = list[0].ID
		}
		for _, d := range list {
			if d.ID == id {
				handler(d)(w, r)
				return
			}
		}
		http.Error(w, "there is no device with this id; GET /api/devices lists them", http.StatusNotFound)
	}
}

// writeJSON answers with body as JSON.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write response", "error", err)
	}
}

func metricsHandler(d Device) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := d.Metrics.Collect(r.Context())
		if errors.Is(err, hub.ErrUnreachable) {
			http.Error(w, "the device has not answered recently", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			slog.Error("collect metrics", "device", d.ID, "error", err)
			http.Error(w, "could not read the machine's usage", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	}
}

// availabilityHandler serves GET /api/availability: how long another device
// did not answer since the hub started collecting from it.
func availabilityHandler(d Device) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Availability == nil {
			http.Error(w, "availability is only kept for the other devices the hub collects from", http.StatusNotFound)
			return
		}
		availability, err := d.Availability.Availability(r.Context())
		if err != nil {
			slog.Error("read availability", "device", d.ID, "error", err)
			http.Error(w, "could not read the availability", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, availability)
	}
}

// websiteHandler serves the website. It is in every language at once and
// switches between them in the browser.
func websiteHandler(site fs.FS) http.Handler {
	if _, err := fs.Stat(site, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
