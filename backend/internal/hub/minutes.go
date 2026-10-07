package hub

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// MinutesPath is where a device serves the averages it keeps of its own
// usage, one per minute, for the hub to fetch: GET /api/minutes?after=<unix
// seconds>[&values=<n>], answered with a MinutesAnswer.
const MinutesPath = "/api/minutes"

// MinutesPerAnswer is how many minutes a device sends at most per answer:
// two hours, some hundred kilobytes.
const MinutesPerAnswer = 120

// ValuesPerAnswer is how many values a device sends at most per answer, in
// all its minutes, but always one minute at least: a few seconds of storing
// on a Raspberry Pi, and well within maxMinutesBytes with the longest metric
// names stored. A device with many disks or network cards sends fewer minutes
// per answer, so its answers neither run out of time nor get too long. A hub
// may ask for fewer with values.
const ValuesPerAnswer = 10_000

// MinutesAnswer is the body of GET /api/minutes. Times are Unix seconds on
// the device's clock.
type MinutesAnswer struct {
	// Now is the device's time when it answered.
	Now     int64            `json:"now"`
	Minutes []history.Minute `json:"minutes"`
	// More tells that more minutes follow the last one.
	More bool `json:"more"`
	// Extras describes the extras among the minutes, by the metric they are
	// stored under, so the hub can draw them though it could not reach the
	// device while they were measured.
	Extras map[string]history.ExtraInfo `json:"extras,omitempty"`
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
	maxMetricLength = history.MaxMetricLength
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
	// maxEntries is how many disks, sensors, network cards and GPUs each, and
	// groups of extras and values in each, the history keeps of the device,
	// as the recorder keeps of its readings.
	maxEntries int
	// retention is how long the hub keeps its history: older minutes are not
	// fetched. Zero fetches any.
	retention time.Duration
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
	// values is how many values one answer has at most; zero is
	// ValuesPerAnswer. It halves after a fetch could not store one answer in
	// time, and doubles back after one that took less than half its time, so
	// it does not go back to a size that only just fits.
	values int
	// tooOld is when the device was found to keep no minutes.
	tooOld time.Time
	// back is when the device last began to answer, as after it was switched
	// off: it keeps its first minute only a minute or two later.
	back time.Time
	// failing is set while fetching fails, so that is logged once.
	failing bool
	// dropped is set once a minute had more entries than are kept, and
	// tooLong once one had names too long, so each is logged once too.
	dropped, tooLong bool
}

// fetch stores the minutes the device has after the newest one fetched, or,
// when it starts again, from a few minutes before the newest one the hub has.
// It returns false when the device does not answer now, is too old to keep
// its minutes, answers but its minutes cannot be fetched, or has not kept
// minute yet, the one the recorder is due to store, so the recorder stores
// the average of its own readings instead; a device minute for the same
// minute is then not stored again.
func (f *fetcher) fetch(ctx context.Context, minute time.Time) bool {
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
	// fetched: the hub starts again from shortly before its newest minute, at
	// the new time.
	deviceNow := time.Now().Add(-measured).Unix()
	if f.after > deviceNow {
		slog.Info("the clock of the device went back; fetching its minutes again from shortly before the newest one the hub has, at the device's new time", "device", f.device)
		f.after = 0
	}

	stopping, began, budget := ctx, time.Now(), cmp.Or(f.budget, fetchFor)
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if f.after == 0 {
		newest, ok, err := f.store.Newest(ctx, f.device)
		if err != nil {
			return f.failed(stopping, ctx, err, false)
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
	// Minutes older than the retention, as after the hub was off for longer,
	// would only be deleted again, and counted in their hour until then.
	if f.retention > 0 {
		f.after = max(f.after, time.Now().Add(-f.retention).Unix()-f.offset)
	}
	start := f.after

	var answer MinutesAnswer
	for range maxAnswersPerFetch {
		answer = MinutesAnswer{}
		err := f.agent.get(ctx, fmt.Sprintf("%s?after=%d&values=%d", f.agent.minutesURL, f.after, f.pageValues()), maxMinutesBytes, &answer)
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
			if err = f.describe(ctx, answer.Extras, minutes); err == nil {
				if err = f.store.AddMinutes(ctx, f.device, minutes); err == nil {
					f.after = after
				}
			}
		}
		if err != nil {
			return f.failed(stopping, ctx, err, f.after != start)
		}
		// Asking again when nothing was new would get the same answer.
		if !answer.More || f.after == previous {
			break
		}
	}
	if f.values != 0 && time.Since(began) < budget/2 {
		f.values = min(ValuesPerAnswer, 2*f.values)
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
		return f.failed(stopping, ctx, errors.New("the device has kept no minute of its usage for a while"), true)
	}
	if f.failing {
		f.failing = false
		slog.Info("fetching the minutes of the device works again", "device", f.device)
	}
	// The hub fetches a few seconds after the device keeps its minute, which
	// a device whose clock is further behind has not done yet. Should it then
	// stop, the minute would be missing: the recorder stores its own, and the
	// device's, fetched next time, is not stored again.
	return !time.Unix(f.after+f.offset, 0).Truncate(history.SampleInterval).Before(minute)
}

// pageValues returns how many values the device is asked to send at most
// per answer.
func (f *fetcher) pageValues() int {
	return cmp.Or(f.values, ValuesPerAnswer)
}

// clean returns the minutes of an answer that are new to the hub, at the
// hub's time on whole minutes, with the values the history keeps, and the
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
		values, dropped, tooLong := keep(minute.Values, f.maxEntries)
		if dropped && !f.dropped {
			f.dropped = true
			slog.Warn("the device kept more disks, sensors, network cards, GPUs or extras than the history keeps; raise HISTORY_MAX_ENTRIES to keep them all",
				"device", f.device, "kept", f.maxEntries)
		}
		if tooLong && !f.tooLong {
			f.tooLong = true
			slog.Warn("the device kept disks, sensors, network cards or GPUs with names too long for the history; they are left out",
				"device", f.device, "maxMetricLength", maxMetricLength)
		}
		if len(values) > 0 {
			minutes = append(minutes, history.Minute{Time: at.Unix(), Values: values})
		}
	}
	return minutes, after
}

// single are the metrics a device has once.
var single = map[string]bool{
	history.MetricCPU: true, history.MetricMemory: true, history.MetricSwap: true, history.MetricBattery: true,
}

// perEntry names, for the metrics that exist once per disk, sensor, network
// card or GPU, what they exist once per, by the longest of its kind's metrics:
// one whose name makes that longer than maxMetricLength is left out with all
// its metrics, as the recorder leaves it out.
var perEntry = map[string]string{
	history.MetricTemperature:    history.MetricTemperature,
	history.MetricDisk:           history.MetricDiskWrite,
	history.MetricDiskRead:       history.MetricDiskWrite,
	history.MetricDiskWrite:      history.MetricDiskWrite,
	history.MetricNetworkReceive: history.MetricNetworkReceive,
	history.MetricNetworkSend:    history.MetricNetworkReceive,
	history.MetricGPU:            history.MetricGPUMemory,
	history.MetricGPUMemory:      history.MetricGPUMemory,
}

// entry is one disk, sensor, network card, GPU, group of extras or extra:
// kind says which (for an extra, its group), name which one.
type entry struct{ kind, name string }

// keep returns the values of one minute the history keeps of a device, as
// the recorder keeps of its readings: the ones it knows, with values that
// can be stored, of at most maxEntries disks, sensors, network cards and GPUs
// each whose names fit in maxMetricLength, and of the extras at most
// history.MaxExtras, of at most maxEntries groups of maxEntries extras each.
// Where there are more, the first by name are kept, so every minute keeps the
// same ones; dropped tells whether any were left out for those limits, and
// tooLong whether any were for their names.
func keep(values map[string]float64, maxEntries int) (kept map[string]float64, dropped, tooLong bool) {
	entries := map[string][]entry{}
	names := map[string]map[string]bool{}
	for metric, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		of, ok, long := entriesOf(metric)
		tooLong = tooLong || long
		if !ok {
			continue
		}
		entries[metric] = of
		for _, e := range of {
			if names[e.kind] == nil {
				names[e.kind] = map[string]bool{}
			}
			names[e.kind][e.name] = true
		}
	}
	keptEntries := map[entry]bool{}
	for kind, set := range names {
		sorted := slices.Sorted(maps.Keys(set))
		if len(sorted) > maxEntries {
			sorted, dropped = sorted[:maxEntries], true
		}
		for _, name := range sorted {
			keptEntries[entry{kind, name}] = true
		}
	}
	kept = map[string]float64{}
	var extras []string
	for metric, of := range entries {
		if slices.ContainsFunc(of, func(e entry) bool { return !keptEntries[e] }) {
			continue
		}
		if strings.HasPrefix(metric, history.MetricExtra+":") {
			extras = append(extras, metric)
		} else {
			kept[metric] = values[metric]
		}
	}
	if len(extras) > history.MaxExtras(maxEntries) {
		slices.Sort(extras)
		extras, dropped = extras[:history.MaxExtras(maxEntries)], true
	}
	for _, metric := range extras {
		kept[metric] = values[metric]
	}
	return kept, dropped, tooLong
}

// entriesOf returns what a metric belongs to, and false for one the history
// does not keep; tooLong tells that is for the name of its disk, sensor,
// network card or GPU.
func entriesOf(metric string) (of []entry, ok, tooLong bool) {
	if single[metric] {
		return nil, true, false
	}
	kind, name, ok := strings.Cut(metric, ":")
	if !ok || name == "" {
		return nil, false, false
	}
	if kind == history.MetricExtra {
		group, item, ok := strings.Cut(name, "/")
		if !ok || !metrics.ValidID(group) || !metrics.ValidID(item) {
			return nil, false, false
		}
		return []entry{{kind, group}, {kind + ":" + group, item}}, true, false
	}
	longest, ok := perEntry[kind]
	if !ok {
		return nil, false, false
	}
	if len(longest)+1+len(name) > maxMetricLength {
		return nil, false, true
	}
	return []entry{{longest, name}}, true, false
}

// describe stores how the extras among minutes are described, as far as the
// hub has no description of them yet, as when they existed only while it
// could not reach the device. They are cut down as the extras of a reading
// are.
func (f *fetcher) describe(ctx context.Context, extras map[string]history.ExtraInfo, minutes []history.Minute) error {
	if len(extras) == 0 {
		return nil
	}
	known, err := f.store.ExtraInfo(ctx, f.device)
	if err != nil {
		return err
	}
	var groups []metrics.Extra
	for _, metric := range slices.Sorted(maps.Keys(extras)) {
		group, item, ok := strings.Cut(strings.TrimPrefix(metric, history.MetricExtra+":"), "/")
		_, isKnown := known[metric]
		if !ok || !strings.HasPrefix(metric, history.MetricExtra+":") || isKnown ||
			!slices.ContainsFunc(minutes, func(m history.Minute) bool { _, ok := m.Values[metric]; return ok }) {
			continue
		}
		info := extras[metric]
		if len(groups) == 0 || groups[len(groups)-1].ID != group {
			groups = append(groups, metrics.Extra{ID: group, Title: info.Title, Titles: info.Titles})
		}
		value := 0.0
		groups[len(groups)-1].Items = append(groups[len(groups)-1].Items, metrics.ExtraItem{
			ID: item, Label: info.Label, Labels: info.Labels, Unit: info.Unit, Value: &value, History: true,
		})
	}
	described := history.DescribeExtras(metrics.CleanExtras(groups, f.maxEntries), f.maxEntries)
	if len(described) == 0 {
		return nil
	}
	return f.store.SetExtraInfo(ctx, f.device, described, time.Now())
}

// failed ends a fetch that failed; stopping is done when the program is
// stopping, and progressed tells whether it stored minutes before it failed.
// When the program is stopping, or the fetch ran out of time after it stored
// some, the rest follows next time. Otherwise it logs the failure, unless the
// previous fetch failed too, and returns false, so the recorder stores the
// average of its own readings for this minute. A fetch that ran out of time
// before it stored one answer asks for half as many values next time, so
// one answer can be stored in time even on a slow disk; that is logged as
// information, and only as a failure once a single minute is too many. The
// next fetch goes on from where this one stopped, so the minutes the device
// kept meanwhile are not lost; one it kept for a minute the recorder stored
// is not stored again. Only when the device refuses where the hub goes on
// from, after its clock went back further than the hub measured, does the
// next fetch start again from shortly before the newest minute the hub has.
func (f *fetcher) failed(stopping, ctx context.Context, err error, progressed bool) bool {
	if stopping.Err() != nil {
		return true
	}
	if ctx.Err() != nil {
		if progressed {
			return true
		}
		if values := f.pageValues(); values > 1 {
			f.values = values / 2
			slog.Info("could not fetch and store one answer of the device's minutes in time; asking for fewer values per answer, storing the average of the hub's own readings for this minute",
				"device", f.device, "values", f.values)
			return false
		}
		err = fmt.Errorf("could not fetch and store one minute in time: %w", err)
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
