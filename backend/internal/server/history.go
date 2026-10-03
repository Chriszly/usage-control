package server

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
)

// HistoryReader reads the machine's usage over time, averaged over steps so
// a range has a few hundred points at most, and returns the step.
type HistoryReader interface {
	Range(ctx context.Context, from, to time.Time) ([]history.Series, time.Duration, error)
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
// the machine's usage in that range, averaged over steps. The range is limited to the retention period
// and ends now at the latest.
func historyHandler(reader HistoryReader, retention time.Duration) http.HandlerFunc {
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

		series, step, err := reader.Range(r.Context(), time.Unix(from, 0), time.Unix(to, 0))
		if err != nil {
			slog.Error("read history", "error", err)
			http.Error(w, "could not read the history", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, historyResponse{
			From:          from,
			To:            to,
			StepSeconds:   int64(step / time.Second),
			RetentionDays: int(retention / (24 * time.Hour)),
			Series:        series,
		})
	}
}
