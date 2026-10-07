package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/hub"
)

// MinuteSource has the averages a machine keeps of its own usage, one per
// minute, for a hub to fetch, and how the extras among them are described:
// its history, or the buffer of a machine without one, which forgets the
// minutes every hub has fetched.
type MinuteSource interface {
	Since(ctx context.Context, hub string, after time.Time, minutes, values int) ([]history.Minute, bool, error)
	ExtraInfo(ctx context.Context) (map[string]history.ExtraInfo, error)
}

// minutesHandler serves GET /api/minutes?after=<unix seconds>[&values=<n>]:
// the oldest minutes after that time, hub.MinutesPerAnswer at most and
// hub.ValuesPerAnswer values in all, or fewer when the hub asks for fewer,
// on this machine's clock, with how the extras among them are described.
// Asking with after tells that the hub has stored everything up to it, so
// after may not be later than this machine's time: there are no minutes
// after it yet. A hub sends hub.PagePortHeader with every request; it is told
// apart from other hubs by the address it asks from. A request without the
// header is not from a hub, so it reads the minutes but deletes none.
func minutesHandler(source MinuteSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().Unix()
		after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if err != nil || after < 0 || after > now {
			http.Error(w, "after must be a Unix time in seconds, not later than this device's time", http.StatusBadRequest)
			return
		}
		values := hub.ValuesPerAnswer
		if asked := r.URL.Query().Get("values"); asked != "" {
			n, err := strconv.Atoi(asked)
			if err != nil || n < 1 {
				http.Error(w, "values must be a whole number of at least 1", http.StatusBadRequest)
				return
			}
			values = min(values, n)
		}
		minutes, more, err := source.Since(r.Context(), hubAsking(r), time.Unix(after, 0), hub.MinutesPerAnswer, values)
		if err != nil {
			slog.Error("read the minutes for a hub", "error", err)
			http.Error(w, "could not read the minutes", http.StatusInternalServerError)
			return
		}
		extras, err := extrasAmong(r.Context(), source, minutes)
		if err != nil {
			slog.Error("read how the extras are described for a hub", "error", err)
			http.Error(w, "could not read the minutes", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, hub.MinutesAnswer{Now: time.Now().Unix(), Minutes: minutes, More: more, Extras: extras})
	}
}

// hubAsking returns the address of the hub a request is from, or empty for
// a request that is not from a hub.
func hubAsking(r *http.Request) string {
	if r.Header.Get(hub.PagePortHeader) == "" {
		return ""
	}
	return askedFrom(r)
}

// askedFrom returns the address a request came from, without its port, which
// changes from one connection to the next.
func askedFrom(r *http.Request) string {
	if sender, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return sender.Addr().Unmap().WithZone("").String()
	}
	return r.RemoteAddr
}

// extrasAmong returns how the extras among minutes are described, or nil
// when they have none.
func extrasAmong(ctx context.Context, source MinuteSource, minutes []history.Minute) (map[string]history.ExtraInfo, error) {
	used := map[string]bool{}
	for _, minute := range minutes {
		for metric := range minute.Values {
			if strings.HasPrefix(metric, history.MetricExtra+":") {
				used[metric] = true
			}
		}
	}
	if len(used) == 0 {
		return nil, nil
	}
	all, err := source.ExtraInfo(ctx)
	if err != nil {
		return nil, err
	}
	extras := map[string]history.ExtraInfo{}
	for metric := range used {
		if info, ok := all[metric]; ok {
			extras[metric] = info
		}
	}
	return extras, nil
}
