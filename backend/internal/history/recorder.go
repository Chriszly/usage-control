package history

import (
	"context"
	"log/slog"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// SampleInterval is how often the recorder stores the average of its
// readings in the database.
const SampleInterval = time.Minute

// storeAt is the second of each minute the recorder stores the average at,
// under that minute. Storing at the same second every minute, not a minute
// after the program started, means a restart of less than about 40 seconds
// never skips a minute: the minute it stops in is stored then, unless it was
// already, and the minute it starts again in is stored at its storeAt,
// unless that passed before it stopped.
const storeAt = 55 * time.Second

// fetchLag is how much later than storeAt a hub's recorder of another device
// stores, at second 5 of the next minute, under the minute before: by then
// the device has kept its minute, which the hub fetches at once. Fetching at
// the device's own storeAt would often find it not kept yet, and lose it
// when the device then died. A device whose clock is so far behind that it
// has not kept its minute by then leaves the minute to the recorder
// (see Fetch).
const fetchLag = 10 * time.Second

// Collector reads the current usage of the machine.
type Collector interface {
	Collect(ctx context.Context) (metrics.Snapshot, error)
}

// Saver keeps the averages a Recorder stores: a Store, or the Buffer of a
// device without a history of its own.
type Saver interface {
	Add(ctx context.Context, device string, at time.Time, values map[string]float64) error
}

// Recorder reads the machine's usage every RecentInterval into Recent, and
// every SampleInterval stores the average of those readings in Store. A
// Pruner deletes what is older than the retention.
type Recorder struct {
	Store     Saver
	Recent    *Recent
	Collector Collector
	// Device is the name the readings are stored under, such as LocalDevice.
	Device string
	// MaxEntries is how many disks, sensors, network cards and GPUs each are
	// kept.
	MaxEntries int
	// Fetch, when set, takes the place of storing the average every
	// SampleInterval: a hub fetches the averages the device keeps itself,
	// which also cover the time the hub could not reach it. minute is the
	// minute the recorder would store under. It returns false for a device
	// too old to keep them, or one whose minutes cannot be fetched now or do
	// not have that minute yet, whose average is then stored as before.
	Fetch func(ctx context.Context, minute time.Time) bool

	// failing is set while readings fail, so an unreachable device is logged
	// once and not every few seconds.
	failing bool
	// dropped is set once a reading had more entries than are kept, so that
	// is logged once too.
	dropped bool
	// tooLong is set once a reading had entries with names too long to keep,
	// so that is logged once too.
	tooLong bool
	// extraInfo writes how the device's extras are described.
	extraInfo extraInfoWriter
}

// Run records until ctx is cancelled. Failures are logged and the next
// reading is tried again, so a full disk does not stop the website.
func (r *Recorder) Run(ctx context.Context) {
	// stored is when the readings not stored yet began, last the minute the
	// last average was stored under, zero before the first.
	stored := time.Now()
	var last time.Time
	// The first reading measures CPU usage since the machine booted and no
	// network speed, so it only starts the first interval and is not kept.
	if _, err := r.Collector.Collect(ctx); err != nil {
		r.failed(err)
	}

	read := time.NewTicker(RecentInterval)
	defer read.Stop()
	wait, due := r.next(stored, last)
	store := time.NewTimer(wait)
	defer store.Stop()
	for {
		select {
		case <-ctx.Done():
			r.storeLast(ctx, stored, last, time.Now())
			return
		case now := <-store.C:
			if minute, ok := r.storedUnder(now, due, last); ok {
				r.store(ctx, stored, now, minute)
				last = minute
			}
			stored = now
			wait, due = r.next(time.Now(), last)
			store.Reset(wait)
		case <-read.C:
			r.read(ctx)
		}
	}
}

// next returns how long until the recorder stores next, and the minute it
// stores under then. That is the minute it was due for, not the one the
// clock shows once the timer fires: the clock may have been set meanwhile,
// as by NTP shortly after a Raspberry Pi without a real-time clock started,
// which would skip a minute or store one twice. For the same reason the
// minute stored last is not due again.
func (r *Recorder) next(now, last time.Time) (time.Duration, time.Time) {
	wait := untilStore(now, r.lag())
	due := r.minuteOf(now.Add(wait))
	if due.Equal(last) {
		wait, due = wait+SampleInterval, due.Add(SampleInterval)
	}
	// After a stall, as when storing took long or the timer fired late, the
	// minute after last can be too close for untilStore, or past already.
	// It is stored as soon as its time comes, while it is still due (see
	// storedUnder), so it gets its point instead of being skipped.
	if !last.IsZero() {
		after := last.Add(SampleInterval)
		at := after.Add(storeAt + r.lag())
		if due.After(after) && now.Sub(at) < SampleInterval {
			return max(at.Sub(now), 0), after
		}
	}
	return wait, due
}

// storedUnder returns the minute the average is stored under when the timer
// fires at now: due, the minute it was set for, as long as the clock shows
// less than a minute off the time it was due at, which a clock set by some
// seconds keeps. A timer runs on the time since it was set, not on the clock, so
// after the clock jumped further, as when it was set by NTP after a
// Raspberry Pi started from a saved time, or the machine resumed from
// suspend, due would be far from the readings; the minute the clock shows is
// taken then, but only when it is after last, the one stored last.
func (r *Recorder) storedUnder(now, due, last time.Time) (time.Time, bool) {
	if off := now.Sub(due.Add(storeAt + r.lag())); off > -SampleInterval && off < SampleInterval {
		return due, true
	}
	minute := r.minuteOf(now)
	return minute, minute.After(last)
}

// lag returns how much later than storeAt the recorder stores.
func (r *Recorder) lag() time.Duration {
	if r.Fetch != nil {
		return fetchLag
	}
	return 0
}

// minuteOf returns the minute an average stored at the given time is stored
// under.
func (r *Recorder) minuteOf(at time.Time) time.Time {
	return at.Add(-r.lag()).Truncate(SampleInterval)
}

// untilStore returns how long until the recorder stores next: lag after
// storeAt of a minute, at least a quarter of a minute from now, so a timer
// that fires a little early, or the start just before then, does not store
// the same minute twice or an average of hardly any readings.
func untilStore(now time.Time, lag time.Duration) time.Duration {
	next := now.Truncate(SampleInterval).Add(storeAt + lag - SampleInterval)
	for next.Sub(now) < SampleInterval/4 {
		next = next.Add(SampleInterval)
	}
	return next.Sub(now)
}

func (r *Recorder) read(ctx context.Context) {
	snapshot, err := r.Collector.Collect(ctx)
	if err != nil {
		r.failed(err)
		return
	}
	if r.failing {
		r.failing = false
		slog.Info("reading usage for the history works again", "device", r.Device)
	}
	// A reading shared with a hub or a page may come round twice. Only the
	// same one is skipped: after the clock was set back, every new reading
	// is older than the newest, and none would be kept until it caught up.
	if newest, ok := r.Recent.Newest(); ok && snapshot.Time.Unix() == newest.Unix() {
		return
	}
	v, dropped, tooLong := values(snapshot, r.MaxEntries)
	if dropped && !r.dropped {
		r.dropped = true
		slog.Warn("the device reports more disks, sensors, network cards, GPUs or values of extras than the history keeps; raise HISTORY_MAX_ENTRIES to keep them all",
			"device", r.Device, "kept", r.MaxEntries)
	}
	if tooLong && !r.tooLong {
		r.tooLong = true
		slog.Warn("the device reports disks, sensors, network cards or GPUs with names too long for the history; they are only shown live",
			"device", r.Device, "maxMetricLength", MaxMetricLength)
	}
	// A history keeps how the extras are described, and so does a data-only
	// device's buffer, for the hub to fetch with the minutes.
	if store, ok := r.Store.(extraInfoSetter); ok {
		if err := r.extraInfo.write(ctx, store, r.Device, extraInfo(snapshot.Extras, r.MaxEntries), snapshot.Time); err != nil {
			slog.Error("store how the extras are described", "device", r.Device, "error", err)
		}
	}
	r.Recent.Add(snapshot.Time, v)
}

// failed logs a failed reading, unless the previous one failed too.
func (r *Recorder) failed(err error) {
	if !r.failing {
		r.failing = true
		slog.Error("read usage for the history", "device", r.Device, "error", err)
	}
}

// storeLast stores the average of the readings from from when the recorder
// stops, as for an update or when the machine is shut down, so a hub fetching
// this device's minutes gets that one too. Only a minute other than last, the
// one stored last, is stored: a buffer replaces a minute it has. A hub's
// recorder of another device leaves it, as the device keeps its own.
func (r *Recorder) storeLast(ctx context.Context, from, last, now time.Time) {
	if r.Fetch != nil || now.Truncate(SampleInterval).Equal(last) {
		return
	}
	averages := r.Recent.Average(from, now)
	if averages == nil {
		return
	}
	if err := r.Store.Add(context.WithoutCancel(ctx), r.Device, now.Truncate(SampleInterval), averages); err != nil {
		slog.Error("store usage in the history", "error", err)
	}
}

// store saves the average of the readings from from up to to under minute,
// the one it was due for (see next): on whole minutes, where a hub also puts
// the minutes it fetches from a device, so a minute both store is stored
// once.
func (r *Recorder) store(ctx context.Context, from, to, minute time.Time) {
	if r.Fetch != nil && r.Fetch(ctx, minute) {
		return
	}
	averages := r.Recent.Average(from, to)
	if averages == nil {
		return
	}
	if err := r.Store.Add(ctx, r.Device, minute, averages); err != nil {
		slog.Error("store usage in the history", "error", err)
	}
}
