package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"slices"
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

const (
	// valuesWithoutHub is how many values a request without a hub id gets at
	// most per answer, so a client that is not a hub cannot have the machine
	// read many thousands of values for each request; a hub of a version
	// that sends no id just asks more often.
	valuesWithoutHub = 1_000
	// maxExtrasBytes is how long the descriptions of the extras in one answer
	// may be in all, as JSON. With the longest metric names in its values,
	// an answer then stays below the 8 MiB a hub reads, even when every
	// value is an extra with the longest texts in every language.
	maxExtrasBytes = 4 << 20
)

// minutesHandler serves GET /api/minutes?after=<unix seconds>[&values=<n>]:
// the oldest minutes after that time, hub.MinutesPerAnswer at most and
// hub.ValuesPerAnswer values in all, or fewer when the hub asks for fewer,
// on this machine's clock, with how the extras among them are described.
// Asking with after tells that the hub has stored everything up to it, so
// after may not be later than this machine's time: there are no minutes
// after it yet. A hub sends its id as hub.HubIDHeader with every request,
// which tells it apart from other hubs. A request without a valid id is not
// from a hub, or from a hub of a version that sends none, so it reads the
// minutes but deletes none, and the buffer keeps them for its span; it gets
// valuesWithoutHub values at most per answer.
func minutesHandler(source MinuteSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().Unix()
		after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if err != nil || after < 0 || after > now {
			http.Error(w, "after must be a Unix time in seconds, not later than this device's time", http.StatusBadRequest)
			return
		}
		hubID := hubAsking(r)
		values := hub.ValuesPerAnswer
		if hubID == "" {
			values = valuesWithoutHub
		}
		if asked := r.URL.Query().Get("values"); asked != "" {
			n, err := strconv.Atoi(asked)
			if err != nil || n < 1 {
				http.Error(w, "values must be a whole number of at least 1", http.StatusBadRequest)
				return
			}
			values = min(values, n)
		}
		minutes, more, err := source.Since(r.Context(), hubID, time.Unix(after, 0), hub.MinutesPerAnswer, values)
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

// hubAsking returns the id of the hub a request is from, or empty for a
// request that is not from a hub.
func hubAsking(r *http.Request) string {
	if id := r.Header.Get(hub.HubIDHeader); hub.ValidHubID(id) {
		return id
	}
	return ""
}

// extrasAmong returns how the extras among minutes are described, or nil
// when they have none. Descriptions beyond maxExtrasBytes are left out, in
// the order of their metrics; the hub then shows those values once the
// device describes them live.
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
	size := 0
	for _, metric := range slices.Sorted(maps.Keys(used)) {
		info, ok := all[metric]
		if !ok {
			continue
		}
		encoded, err := json.Marshal(info)
		if err != nil {
			return nil, err
		}
		// The metric as the key, with its quotes, colon and comma.
		size += len(encoded) + len(metric) + 4
		if size > maxExtrasBytes {
			break
		}
		extras[metric] = info
	}
	return extras, nil
}
