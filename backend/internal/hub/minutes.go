package hub

import (
	"cmp"
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
	// maxAnswersPerFetch is how many answers one fetch reads at most. Ten
	// answers are 20 hours.
	maxAnswersPerFetch = 10
	// fetchFor is how long one fetch takes at most. The recorder waits for
	// it, so it misses two of its readings at most, too few to make a gap in
	// them (see history.Recent.Covers); the rest follows a minute later.
	fetchFor = 10 * time.Second
	// recheckAfter is how long a device too old to keep its minutes is
	// stored the hub's way before it is asked again, as it may be updated.
	recheckAfter = time.Hour
	// maxMetricLength is the longest metric name stored.
	maxMetricLength = 256
	// maxClockJitter is how far the difference between the hub's clock and the
	// device's may move before the minutes are moved by the new one. Measured
	// with each reading, it varies by a second or two, which would now and
	// then put a minute in the place of the one before or after it.
	maxClockJitter = 10 * time.Second
	// keptWithin is how far behind its time the newest minute a device kept
	// may be: a device keeps each minute within the next one, so up to two
	// minutes, and up to about five when its program just restarted; more
	// means it keeps none now.
	keptWithin = 5 * time.Minute
)

// fetcher fetches the minutes a device keeps of its own usage into the hub's
// history: every minute the newest one, and after the hub could not reach the
// device, or was not running, the ones it missed meanwhile. Times are moved
// from the device's clock to the hub's, as for the readings, and put on whole
// minutes, where the recorder stores its own.
type fetcher struct {
	agent  *Agent
	store  *history.Store
	device string
	// maxValues is how many values of one minute are stored at most.
	maxValues int
	// budget, when set, is how long one fetch takes at most instead of
	// fetchFor, for tests.
	budget time.Duration

	// after is the time, on the device's clock, of the newest minute fetched
	// from the device; zero until a fetch works it out from the database, as
	// at the start, once an older device was updated, and after the device
	// refused it or its clock went back.
	after int64
	// offset is how many seconds the hub's clock is ahead of the device's, as
	// the minutes are moved by; see maxClockJitter.
	offset int64
	// tooOld is when the device was found to keep no minutes.
	tooOld time.Time
	// back is when the device last began to answer, as after it was switched
	// off: it keeps its first minute only a minute or two later.
	back time.Time
	// failing is set while fetching fails, so that is logged once.
	failing bool
}

// fetch stores the minutes the device has after the newest one the hub has.
// It returns false when the device does not answer now, is too old to keep
// its minutes, or answers but its minutes cannot be fetched, so the recorder
// stores the average of its own readings instead; a device minute for the
// same minute is then not stored again.
func (f *fetcher) fetch(ctx context.Context) bool {
	if !f.tooOld.IsZero() && time.Since(f.tooOld) < recheckAfter {
		return false
	}
	// A device that does not answer has nothing to fetch now. The recorder
	// stores the readings it has of this minute, as when the device was
	// switched off during it, and keeps none of its own; once the device
	// answers again, the minutes it kept meanwhile follow.
	measured, ok := f.agent.clockOffset()
	if !ok {
		f.back = time.Time{}
		return false
	}
	if f.back.IsZero() {
		f.back = time.Now()
	}
	if moved := measured - time.Duration(f.offset)*time.Second; moved > maxClockJitter || moved < -maxClockJitter {
		f.offset = int64(measured / time.Second)
	}
	// The device's time as of its newest reading. A clock that went back has
	// its new minutes before the ones the hub has, so those would never be
	// fetched: the hub starts again from its newest minute at the new time.
	deviceNow := time.Now().Add(-measured).Unix()
	if f.after > deviceNow {
		slog.Info("the clock of the device went back; fetching its minutes from the newest one the hub has, at the device's new time", "device", f.device)
		f.after = 0
	}

	ctx, cancel := context.WithTimeout(ctx, cmp.Or(f.budget, fetchFor))
	defer cancel()
	if f.after == 0 {
		newest, ok, err := f.store.Newest(ctx, f.device)
		if err != nil {
			return f.failed(ctx, err)
		}
		if ok {
			// A few minutes before the hub's newest, which may be one the hub
			// stored itself while the device did not answer, before it fetched
			// the one the device kept just before; minutes the hub has are not
			// stored again.
			newest = newest.Add(-keptWithin)
		} else {
			// A device new to the hub starts now, not with what it kept for another.
			newest = time.Now().Add(-history.SampleInterval)
		}
		// Not later than the device's time, which it refuses, with room for the
		// difference of the clocks to vary.
		f.after = min(newest.Unix()-f.offset, deviceNow-int64(maxClockJitter/time.Second))
	}

	var answer MinutesAnswer
	for range maxAnswersPerFetch {
		answer = MinutesAnswer{}
		err := f.agent.get(ctx, fmt.Sprintf("%s?after=%d", f.agent.minutesURL, f.after), maxMinutesBytes, &answer)
		var status *statusError
		if errors.As(err, &status) && status.Code == http.StatusNotFound {
			if f.tooOld.IsZero() {
				slog.Info("the device runs an older version that keeps no minutes for the hub; update it so the hub's history has no gaps when it cannot reach it", "device", f.device)
			}
			f.tooOld = time.Now()
			f.after = 0
			return false
		}
		f.tooOld = time.Time{}
		previous := f.after
		if err == nil {
			minutes, after := f.clean(answer)
			if err = f.store.AddMinutes(ctx, f.device, minutes); err == nil {
				f.after = after
			}
		}
		if err != nil {
			return f.failed(ctx, err)
		}
		// Asking again when nothing was new would get the same answer.
		if !answer.More || f.after == previous {
			break
		}
	}
	// A device that answers but has kept no minute for a while, as when it
	// cannot write to its disk, would leave a gap: the recorder stores the
	// average of its own readings instead. A device that just began to answer,
	// as after it was switched on, keeps its first minute a minute or two
	// later, so that is only logged once it had the time to.
	if !answer.More && answer.Now-f.after > int64(keptWithin/time.Second) {
		if time.Since(f.back) <= keptWithin {
			return false
		}
		return f.failed(ctx, errors.New("the device has kept no minute of its usage for a while"))
	}
	if f.failing {
		f.failing = false
		slog.Info("fetching the minutes of the device works again", "device", f.device)
	}
	return true
}

// clean returns the minutes of an answer that are new to the hub, at the
// hub's time on whole minutes, without values that cannot be stored, and the
// device's time of the last of them, which after becomes once they are
// stored. The device is not trusted to keep to its side: minutes must be in
// order, a good part of a minute apart and not in its future. A minute in the
// hub's future, where a clock a few seconds ahead of the hub's or set forward
// since its last reading puts it, waits for a later fetch with the ones after
// it.
func (f *fetcher) clean(answer MinutesAnswer) ([]history.Minute, int64) {
	minGap := int64(history.SampleInterval / time.Second / 2)
	after := f.after
	var minutes []history.Minute
	for _, minute := range answer.Minutes {
		if minute.Time < after+minGap || minute.Time > answer.Now {
			continue
		}
		at := time.Unix(minute.Time+f.offset, 0).Truncate(history.SampleInterval)
		if at.After(time.Now()) {
			break
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
			minutes = append(minutes, history.Minute{Time: at.Unix(), Values: values})
		}
	}
	return minutes, after
}

// failed ends a fetch that failed. When it ran out of time, or the program is
// stopping, the rest follows next time. Otherwise it logs the failure, unless
// the previous fetch failed too, and returns false, so the recorder stores
// the average of its own readings for this minute. The next fetch goes on
// from where this one stopped, so the minutes the device kept meanwhile are
// not lost; one it kept for a minute the recorder stored is not stored again.
// Only when the device refuses where the hub goes on from, after its clock
// went back further than the hub measured, does the next fetch start again
// from the newest minute the hub has.
func (f *fetcher) failed(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	if !f.failing {
		f.failing = true
		slog.Error("fetch the minutes of the device; storing the average of the hub's own readings meanwhile", "device", f.device, "error", err)
	}
	var status *statusError
	if errors.As(err, &status) && status.Code == http.StatusBadRequest {
		f.after = 0
	}
	return false
}

// maxValues returns how many values of one minute are stored at most, when
// the history keeps historyEntries disks, sensors, network cards and GPUs
// each: room for every value of each, with plenty to spare.
func maxValues(historyEntries int) int {
	return historyEntries * (historyEntries + 16)
}
