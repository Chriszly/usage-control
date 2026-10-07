package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"sync"

	"github.com/Chriszly/usage-control/backend/internal/hub"
	"github.com/Chriszly/usage-control/backend/internal/password"
)

// maxChangeBytes is the largest request body that adds or removes a device.
const maxChangeBytes = 4096

// Hub adds and removes the other devices the site collects from. Problems
// with the device asked for are hub.InputErrors.
type Hub interface {
	Add(ctx context.Context, name, address string, kind hub.Kind) (hub.Device, error)
	Remove(ctx context.Context, id string, keepHistory bool) error
	// SetKind changes what a device is used as.
	SetKind(ctx context.Context, id string, kind hub.Kind) error
	// Suggest returns the device at from, the address of a visitor, to offer
	// adding it; false when there is none to offer.
	// own lists the machine's addresses from its usage reading, which are the
	// host's in a container.
	Suggest(ctx context.Context, from netip.Addr, own []netip.Addr) (hub.Suggestion, bool)
}

// Password guards adding and removing devices. It is chosen with the first
// change and stays the same after that.
type Password interface {
	IsSet(ctx context.Context) (bool, error)
	Check(ctx context.Context, password string) error
	Set(ctx context.Context, password string) error
}

// Problems with the password, next to the hub.Problem values.
const (
	problemPasswordLength = "passwordLength"
	problemWrongPassword  = "wrongPassword"
	problemRequest        = "request"
)

// problemResponse is the body of a refused change. Problem names what is
// wrong, so the page can explain it in the visitor's language; Message
// explains it in English for everyone else.
type problemResponse struct {
	Problem string `json:"problem"`
	Message string `json:"message"`
}

// deviceChanges serves adding and removing devices.
type deviceChanges struct {
	hub      Hub
	password Password
	// local reads the usage of the machine the site runs on, whose network
	// addresses are never suggested.
	local Collector

	// mu makes changes happen one at a time, so two first changes cannot
	// both choose the password.
	mu sync.Mutex
}

type addRequest struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	// Kind is "server" or "pc"; a server when left out.
	Kind     string `json:"kind"`
	Password string `json:"password"`
}

type kindRequest struct {
	Kind     string `json:"kind"`
	Password string `json:"password"`
}

type removeRequest struct {
	Password    string `json:"password"`
	KeepHistory bool   `json:"keepHistory"`
}

// add serves POST /api/devices: adds the device in the body.
func (c *deviceChanges) add(w http.ResponseWriter, r *http.Request) {
	var request addRequest
	if !readJSON(w, r, &request) {
		return
	}
	c.change(w, r, request.Password, func() (any, error) {
		kind, err := hub.ParseKind(request.Kind)
		if err != nil {
			return nil, err
		}
		device, err := c.hub.Add(r.Context(), request.Name, request.Address, kind)
		return Device{ID: device.ID, Name: device.Name, Address: device.Address, Kind: kind, Removable: true}, err
	})
}

// setKind serves PUT /api/devices/{id}/kind: changes what a device is used as.
func (c *deviceChanges) setKind(w http.ResponseWriter, r *http.Request) {
	var request kindRequest
	if !readJSON(w, r, &request) {
		return
	}
	c.change(w, r, request.Password, func() (any, error) {
		kind, err := hub.ParseKind(request.Kind)
		if err != nil {
			return nil, err
		}
		return nil, c.hub.SetKind(r.Context(), r.PathValue("id"), kind)
	})
}

// remove serves DELETE /api/devices/{id}: removes a device added on the page.
func (c *deviceChanges) remove(w http.ResponseWriter, r *http.Request) {
	var request removeRequest
	if !readJSON(w, r, &request) {
		return
	}
	c.change(w, r, request.Password, func() (any, error) {
		return nil, c.hub.Remove(r.Context(), r.PathValue("id"), request.KeepHistory)
	})
}

// suggest serves GET /api/devices/suggestion: the device the page is opened
// on, to offer adding it, or 204 No Content when there is none to offer.
func (c *deviceChanges) suggest(w http.ResponseWriter, r *http.Request) {
	// localNetworkOnly has already read the sender's address.
	sender, _ := netip.ParseAddrPort(r.RemoteAddr)
	from := sender.Addr().Unmap()
	suggestion, ok := c.hub.Suggest(r.Context(), from, c.ownAddresses(r.Context()))
	if !ok {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, suggestion)
}

// ownAddresses lists the addresses of the machine's network cards from its
// usage. In a container, the machine's own addresses are not the container's,
// but the usage lists them: a browser on the machine itself can show up with
// one of them.
func (c *deviceChanges) ownAddresses(ctx context.Context) []netip.Addr {
	snapshot, err := c.local.Collect(ctx)
	if err != nil {
		return nil
	}
	var own []netip.Addr
	for _, network := range snapshot.Network {
		for _, address := range network.Addresses {
			if addr, err := netip.ParseAddr(address); err == nil {
				own = append(own, addr.Unmap())
			}
		}
	}
	return own
}

// change makes a change once the password is right. Without a password yet,
// the change chooses it, but only when the change works.
func (c *deviceChanges) change(w http.ResponseWriter, r *http.Request, given string, makeChange func() (any, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.password.Check(r.Context(), given)
	choose := errors.Is(err, password.ErrNotSet)
	if choose {
		err = password.CheckNew(given)
	}
	if err != nil {
		writeProblem(w, err)
		return
	}

	result, err := makeChange()
	if err != nil {
		writeProblem(w, err)
		return
	}
	if choose {
		if err := c.password.Set(r.Context(), given); err != nil {
			writeProblem(w, err)
			return
		}
		slog.Info("the password for changing devices has been chosen")
	}
	if result == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// readJSON reads a small JSON request body into v. Only JSON is accepted: a
// browser does not send it from another site without asking this one first,
// and this one never allows it, so other web pages cannot change devices.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, problemResponse{problemRequest, "send the request as application/json"})
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChangeBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, problemResponse{problemRequest, "the request is not valid JSON: " + err.Error()})
		return false
	}
	return true
}

// writeProblem answers a refused change with what is wrong.
func writeProblem(w http.ResponseWriter, err error) {
	var input *hub.InputError
	switch {
	case errors.As(err, &input):
		writeJSON(w, inputStatus(input.Problem), problemResponse{string(input.Problem), input.Message})
	case errors.Is(err, password.ErrWrong):
		writeJSON(w, http.StatusForbidden, problemResponse{problemWrongPassword, "the password is wrong"})
	case errors.Is(err, password.ErrLength):
		writeJSON(w, http.StatusBadRequest, problemResponse{problemPasswordLength, err.Error()})
	default:
		slog.Error("change devices", "error", err)
		http.Error(w, "could not change the devices", http.StatusInternalServerError)
	}
}

func inputStatus(problem hub.Problem) int {
	switch problem {
	case hub.ProblemNotFound:
		return http.StatusNotFound
	case hub.ProblemNameTaken, hub.ProblemAddressTaken, hub.ProblemFixed, hub.ProblemRemoving:
		return http.StatusConflict
	case hub.ProblemUnreachable:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusBadRequest
	}
}
