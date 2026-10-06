package server

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/hub"
)

// MinuteSource has the averages a machine keeps of its own usage, one per
// minute, for a hub to fetch: its history, or the buffer of a machine without
// one, which forgets the minutes a hub has fetched.
type MinuteSource interface {
	Since(ctx context.Context, after time.Time, limit int) ([]history.Minute, bool, error)
}

// minutesHandler serves GET /api/minutes?after=<unix seconds>: the oldest
// minutes after that time, hub.MinutesPerAnswer at most, on this machine's
// clock. Asking with after tells that the hub has stored everything up to it.
func minutesHandler(source MinuteSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if err != nil || after < 0 {
			http.Error(w, "after must be a Unix time in seconds", http.StatusBadRequest)
			return
		}
		minutes, more, err := source.Since(r.Context(), time.Unix(after, 0), hub.MinutesPerAnswer)
		if err != nil {
			slog.Error("read the minutes for a hub", "error", err)
			http.Error(w, "could not read the minutes", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, hub.MinutesAnswer{Now: time.Now().Unix(), Minutes: minutes, More: more})
	}
}
