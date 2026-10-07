package server

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
)

// HistoryReader reads the machine's usage over time, averaged over steps so
// a range has a few hundred points at most, and returns the step. Newest
// tells when the newest reading is from, false when there is none.
type HistoryReader interface {
	Range(ctx context.Context, from, to time.Time) ([]history.Series, time.Duration, error)
	Newest(ctx context.Context) (time.Time, bool, error)
	ExtraInfo(ctx context.Context) (map[string]history.ExtraInfo, error)
}

// historyResponse is the body of GET /api/history. Times are Unix seconds.
type historyResponse struct {
	From          int64            `json:"from"`
	To            int64            `json:"to"`
	StepSeconds   int64            `json:"stepSeconds"`
	RetentionDays int              `json:"retentionDays"`
	Series        []history.Series `json:"series"`
	// LastReading is set for a device that is not answering: when its newest
	// reading is from. The range then ends there instead of now.
	LastReading int64 `json:"lastReading,omitempty"`
	// Extras describes the series of extras, by metric.
	Extras map[string]history.ExtraInfo `json:"extras,omitempty"`
}

// historyHandler serves GET /api/history?from=<unix seconds>&to=<unix seconds>:
// the machine's usage in that range, averaged over steps. The range is limited to the retention period
// and ends now at the latest. For a device that is not answering, a range
// that ends after its newest reading is moved back to end there, keeping its
// length, so the page shows the last data there is instead of nothing.
func historyHandler(d Device, retention time.Duration) http.HandlerFunc {
	reader := d.History
	return func(w http.ResponseWriter, r *http.Request) {
		from, fromErr := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
		to, toErr := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
		if fromErr != nil || toErr != nil || from >= to {
			http.Error(w, "from and to must be Unix times in seconds, with from before to", http.StatusBadRequest)
			return
		}
		now := time.Now()
		from = min(max(from, now.Add(-retention).Unix()), now.Unix())
		to = min(max(to, from+1), now.Unix()+1)

		var lastReading int64
		if d.Unreachable {
			newest, ok, err := reader.Newest(r.Context())
			if err != nil {
				slog.Error("read the newest reading", "device", d.ID, "error", err)
				http.Error(w, "could not read the history", http.StatusInternalServerError)
				return
			}
			if ok {
				lastReading = newest.Unix()
				if lastReading+1 < to {
					span := to - from
					to = lastReading + 1
					from = min(max(to-span, now.Add(-retention).Unix()), to-1)
				}
			}
		}

		series, step, err := reader.Range(r.Context(), time.Unix(from, 0), time.Unix(to, 0))
		if err != nil {
			slog.Error("read history", "error", err)
			http.Error(w, "could not read the history", http.StatusInternalServerError)
			return
		}

		series, extras, err := extrasOf(r.Context(), reader, series)
		if err != nil {
			slog.Error("read how the extras are described", "device", d.ID, "error", err)
			http.Error(w, "could not read the history", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, historyResponse{
			From:          from,
			To:            to,
			StepSeconds:   int64(step / time.Second),
			RetentionDays: int(retention / (24 * time.Hour)),
			Series:        series,
			LastReading:   lastReading,
			Extras:        extras,
		})
	}
}

// extrasOf returns the descriptions of the extras among series, or nil when
// there are none, so devices without extras need no lookup. Series of extras
// whose description is missing are left out, as the page could not tell what
// they are.
func extrasOf(ctx context.Context, reader HistoryReader, series []history.Series) ([]history.Series, map[string]history.ExtraInfo, error) {
	if !slices.ContainsFunc(series, isExtra) {
		return series, nil, nil
	}
	info, err := reader.ExtraInfo(ctx)
	if err != nil {
		return nil, nil, err
	}
	// A new slice, as series may be shared with a cache.
	shown := make([]history.Series, 0, len(series))
	extras := map[string]history.ExtraInfo{}
	for _, s := range series {
		if isExtra(s) {
			description, ok := info[s.Metric]
			if !ok {
				continue
			}
			extras[s.Metric] = description
		}
		shown = append(shown, s)
	}
	return shown, extras, nil
}

func isExtra(s history.Series) bool {
	return strings.HasPrefix(s.Metric, history.MetricExtra+":")
}
