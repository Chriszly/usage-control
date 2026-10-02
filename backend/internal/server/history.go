package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
)

// maxPoints is the most points a history response has per metric. Longer
// ranges are averaged over longer steps, so a 30 day range stays small.
const maxPoints = 360

// HistoryReader reads stored usage over time.
type HistoryReader interface {
	Range(ctx context.Context, device string, from, to time.Time, step time.Duration) ([]history.Series, error)
}

// History is where the usage over time is read from and how long it is kept.
type History struct {
	Reader    HistoryReader
	Retention time.Duration
}

// historyResponse is the body of GET /api/history. Times are Unix seconds.
type historyResponse struct {
	From          int64            `json:"from"`
	To            int64            `json:"to"`
	StepSeconds   int64            `json:"stepSeconds"`
	RetentionDays int              `json:"retentionDays"`
	Series        []history.Series `json:"series"`
}

// historyHandler serves GET /api/history?from=<unix seconds>&to=<unix seconds>:
// the machine's usage in that range, averaged over steps so there are at most
// maxPoints points per metric. The range is limited to the retention period
// and ends now at the latest.
func historyHandler(h History) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, fromErr := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
		to, toErr := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
		if fromErr != nil || toErr != nil || from >= to {
			http.Error(w, "from and to must be Unix times in seconds, with from before to", http.StatusBadRequest)
			return
		}
		now := time.Now()
		from = min(max(from, now.Add(-h.Retention).Unix()), now.Unix())
		to = min(max(to, from+1), now.Unix()+1)

		step := stepFor(time.Duration(to-from) * time.Second)
		series, err := h.Reader.Range(r.Context(), history.LocalDevice, time.Unix(from, 0), time.Unix(to, 0), step)
		if err != nil {
			slog.Error("read history", "error", err)
			http.Error(w, "could not read the history", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		err = json.NewEncoder(w).Encode(historyResponse{
			From:          from,
			To:            to,
			StepSeconds:   int64(step / time.Second),
			RetentionDays: int(h.Retention / (24 * time.Hour)),
			Series:        series,
		})
		if err != nil {
			slog.Error("write history response", "error", err)
		}
	}
}

// stepFor returns the step that splits span into at most maxPoints steps: a
// whole number of sample intervals, so every step averages the same number of
// samples.
func stepFor(span time.Duration) time.Duration {
	samples := (span/history.SampleInterval + maxPoints - 1) / maxPoints
	return max(1, samples) * history.SampleInterval
}
