package hub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
)

// MinutesPath is where a device serves the averages it keeps of its own
// usage, one per minute, for the hub to fetch: GET /api/minutes?after=<unix
// seconds>, answered with a MinutesAnswer.
const MinutesPath = "/api/minutes"

// MinutesPerAnswer is how many minutes a device sends at most per answer:
// two hours, some hundred kilobytes.
const MinutesPerAnswer = 120

// MinutesAnswer is the body of GET /api/minutes. Times are Unix seconds on
// the device's clock.
type MinutesAnswer struct {
	// Now is the device's time when it answered.
	Now     int64            `json:"now"`
	Minutes []history.Minute `json:"minutes"`
	// More tells that more minutes follow the last one.
	More bool `json:"more"`
}

const (
	// maxMinutesBytes is the largest answer to GET /api/minutes that is read.
	maxMinutesBytes = 8 << 20
	// maxAnswersPerFetch is how many answers one fetch reads at most, so the
	// recorder soon gets back to its readings; the rest follows a minute
	// later. Ten answers are 20 hours.
	maxAnswersPerFetch = 10
	// recheckAfter is how long a device too old to keep its minutes is
	// stored the hub's way before it is asked again, as it may be updated.
	recheckAfter = time.Hour
	// maxMetricLength is the longest metric name stored.
	maxMetricLength = 256
)

// fetcher fetches the minutes a device keeps of its own usage into the hub's
// history: every minute the newest one, and after the hub could not reach the
// device, or was not running, the ones it missed meanwhile. Times are moved
// from the device's clock to the hub's, as for the readings.
type fetcher struct {
	agent  *Agent
	store  *history.Store
	device string
	// maxValues is how many values of one minute are stored at most.
	maxValues int

	// after is the time, on the device's clock, of the newest minute the hub
	// has; zero until the first fetch works it out from the database.
	after int64
	// tooOld is when the device was found to keep no minutes.
	tooOld time.Time
	// failing is set while fetching fails, so that is logged once.
	failing bool
}

// fetch stores the minutes the device has after the newest one the hub has.
// It returns false when the device is too old to keep its minutes, so the
// recorder stores the average of its own readings instead.
func (f *fetcher) fetch(ctx context.Context) bool {
	if !f.tooOld.IsZero() && time.Since(f.tooOld) < recheckAfter {
		return false
	}
	// A device that does not answer has nothing to fetch now; once it
	// answers again, the minutes it kept meanwhile follow.
	offset, ok := f.agent.clockOffset()
	if !ok {
		return true
	}
	if f.after == 0 {
		newest, ok, err := f.store.Newest(ctx, f.device)
		if err != nil {
			f.failed(err)
			return true
		}
		if !ok {
			// A device new to the hub starts now, not with what it kept for another.
			newest = time.Now().Add(-history.SampleInterval)
		}
		f.after = newest.Add(-offset).Unix()
	}

	for range maxAnswersPerFetch {
		var answer MinutesAnswer
		err := f.agent.get(ctx, fmt.Sprintf("%s?after=%d", f.agent.minutesURL, f.after), maxMinutesBytes, &answer)
		var status *statusError
		if errors.As(err, &status) && status.Code == http.StatusNotFound {
			if f.tooOld.IsZero() {
				slog.Info("the device runs an older version that keeps no minutes for the hub; update it so the hub's history has no gaps when it cannot reach it", "device", f.device)
			}
			f.tooOld = time.Now()
			return false
		}
		if err == nil {
			minutes, after := f.clean(answer)
			if err = f.store.AddMinutes(ctx, f.device, minutes); err == nil {
				f.after = after
			}
		}
		if err != nil {
			f.failed(err)
			return true
		}
		if f.failing {
			f.failing = false
			slog.Info("fetching the minutes of the device works again", "device", f.device)
		}
		if !answer.More {
			break
		}
	}
	f.tooOld = time.Time{}
	return true
}

// clean returns the minutes of an answer that are new to the hub, at the
// hub's time, without values that cannot be stored, and the device's time of
// the last of them, which after becomes once they are stored. The device is not trusted to keep to its side: minutes must be in
// order, a good part of a minute apart and not in its future.
func (f *fetcher) clean(answer MinutesAnswer) ([]history.Minute, int64) {
	offset := time.Now().Unix() - answer.Now
	minGap := int64(history.SampleInterval / time.Second / 2)
	after := f.after
	var minutes []history.Minute
	for _, minute := range answer.Minutes {
		if minute.Time < after+minGap || minute.Time > answer.Now {
			continue
		}
		after = minute.Time
		values := map[string]float64{}
		for metric, value := range minute.Values {
			if len(values) == f.maxValues {
				break
			}
			if metric != "" && len(metric) <= maxMetricLength && !math.IsNaN(value) && !math.IsInf(value, 0) {
				values[metric] = value
			}
		}
		if len(values) > 0 {
			minutes = append(minutes, history.Minute{Time: minute.Time + offset, Values: values})
		}
	}
	return minutes, after
}

// failed logs a failed fetch, unless the previous one failed too.
func (f *fetcher) failed(err error) {
	if !f.failing {
		f.failing = true
		slog.Error("fetch the minutes of the device", "device", f.device, "error", err)
	}
}

// maxValues returns how many values of one minute are stored at most, when
// the history keeps historyEntries disks, sensors, network cards and GPUs
// each: room for every value of each, with plenty to spare.
func maxValues(historyEntries int) int {
	return historyEntries * (historyEntries + 16)
}
