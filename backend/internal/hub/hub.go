package hub

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
)

// savedSchema keeps the devices added on the page. Devices from HUB_DEVICES
// are not stored; they come from the setting at every start.
const savedSchema = `
CREATE TABLE IF NOT EXISTS hub_devices (
	id      TEXT    PRIMARY KEY,
	name    TEXT    NOT NULL,
	address TEXT    NOT NULL,
	added   INTEGER NOT NULL -- Unix time in seconds
);
`

// Hub collects the usage of the other devices: the ones in HUB_DEVICES and
// the ones added on the page. Each has a recorder that keeps its history.
type Hub struct {
	store *history.Store
	// historyEntries is how many disks, sensors, network cards and GPUs each
	// the history keeps per device.
	historyEntries int
	// ctx ends every recorder when the program stops.
	ctx       context.Context
	recording sync.WaitGroup

	// mu guards remotes.
	mu      sync.Mutex
	remotes []*Remote
}

// Remote is another device the hub collects from.
type Remote struct {
	Device
	// Fixed is set for a device from HUB_DEVICES, which the page cannot remove.
	Fixed  bool
	Agent  *Agent
	Reader history.Reader

	db *sql.DB
	// watched is the recorder's view of the device, which knows an outage
	// that lasts.
	watched  *watchedAgent
	stop     context.CancelFunc
	recorded chan struct{}
}

// New starts collecting from the fixed devices and the ones added on the page
// earlier, until ctx is done. The history is kept in store, with the first
// historyEntries disks, sensors, network cards and GPUs each of a device.
func New(ctx context.Context, store *history.Store, fixed []Device, historyEntries int) (*Hub, error) {
	h := &Hub{store: store, historyEntries: historyEntries, ctx: ctx}
	for _, schema := range []string{savedSchema, availabilitySchema} {
		if _, err := store.DB().ExecContext(ctx, schema); err != nil {
			return nil, err
		}
	}
	// Devices added before availability was tracked count from when they were added.
	if _, err := store.DB().ExecContext(ctx, `INSERT OR IGNORE INTO hub_watched (device, since) SELECT id, added FROM hub_devices`); err != nil {
		return nil, err
	}
	saved, err := h.saved(ctx)
	if err != nil {
		return nil, err
	}
	// A device dropped from HUB_DEVICES without being added on the page is
	// gone for good: its availability is deleted, as when it is removed on the
	// page, while its history ages out with the retention.
	var kept []string
	for _, device := range slices.Concat(fixed, saved) {
		kept = append(kept, device.ID)
	}
	if err := forgetOthers(ctx, store.DB(), kept); err != nil {
		return nil, err
	}
	for _, device := range fixed {
		h.start(device, true)
	}
	for _, device := range saved {
		if h.find(device.ID) != nil {
			slog.Warn("a device added on the page has the same name as one in HUB_DEVICES; using the one in HUB_DEVICES", "name", device.Name)
			continue
		}
		h.start(device, false)
	}
	return h, nil
}

// Remotes returns the devices the hub collects from, in the order they were added.
func (h *Hub) Remotes() []*Remote {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.remotes)
}

// Add checks the device, asks it for its usage once to make sure a
// usage-control answers at its address, keeps it in the database and starts
// collecting from it. Problems with the device are InputErrors.
func (h *Hub) Add(ctx context.Context, name, address string) (Device, error) {
	device, err := NewDevice(name, address)
	if err != nil {
		return Device{}, err
	}

	// Asked before taking mu, so the page is not held up while the device answers.
	if _, err := NewAgent(device.Address).Collect(ctx); err != nil {
		return Device{}, &InputError{Problem: ProblemUnreachable, Message: fmt.Sprintf("no usage-control answers at %s: %v", device.Address, err)}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.find(device.ID) != nil {
		return Device{}, &InputError{Problem: ProblemNameTaken, Message: "another device has the same name; give each device its own name"}
	}
	_, err = h.store.DB().ExecContext(ctx, `INSERT INTO hub_devices (id, name, address, added) VALUES (?, ?, ?, ?)`,
		device.ID, device.Name, device.Address, time.Now().Unix())
	if err != nil {
		return Device{}, err
	}
	h.start(device, false)
	return device, nil
}

// Remove stops collecting from a device added on the page and forgets it.
// Its history and availability are deleted too, unless keepHistory is set;
// adding a device with the same name later continues them.
func (h *Hub) Remove(ctx context.Context, id string, keepHistory bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	remote := h.find(id)
	if remote == nil {
		return &InputError{Problem: ProblemNotFound, Message: "there is no device with this id"}
	}
	if remote.Fixed {
		return &InputError{Problem: ProblemFixed, Message: "the device is set in HUB_DEVICES; remove it there"}
	}

	// Once the device is deleted from the database, the rest follows even
	// when the page that asked has gone away, so nothing is left half removed.
	ctx = context.WithoutCancel(ctx)
	if _, err := h.store.DB().ExecContext(ctx, `DELETE FROM hub_devices WHERE id = ?`, id); err != nil {
		return err
	}
	h.remotes = slices.DeleteFunc(h.remotes, func(r *Remote) bool { return r == remote })
	remote.stop()
	<-remote.recorded
	slog.Info("stopped collecting from another device", "name", remote.Name, "historyKept", keepHistory)
	if keepHistory {
		return nil
	}
	if err := forget(ctx, h.store.DB(), id); err != nil {
		return err
	}
	return h.store.DeleteDevice(ctx, id)
}

// Availability tells how long the device did not answer since it was added.
func (r *Remote) Availability(ctx context.Context) (Availability, error) {
	availability, err := readAvailability(ctx, r.db, r.ID)
	if err != nil {
		return Availability{}, err
	}
	// An outage that lasts is in the database only up to its last write; its
	// current end is known in memory.
	if outage, unwritten, ok := r.watched.ongoing(); ok {
		availability.LastOutage = &outage
		availability.OfflineSeconds += int64(unwritten / time.Second)
	}
	return availability, nil
}

// Wait waits until every recorder has stopped after the ctx given to New is done.
func (h *Hub) Wait() {
	h.recording.Wait()
}

// start begins collecting from a device. The caller holds mu, or New runs.
func (h *Hub) start(device Device, fixed bool) {
	agent := NewAgent(device.Address)
	if err := watch(h.ctx, h.store.DB(), device.ID, time.Now()); err != nil {
		slog.Error("store when the hub started collecting from a device", "name", device.Name, "error", err)
	}
	recent := &history.Recent{}
	watched := &watchedAgent{agent: agent, db: h.store.DB(), device: device.ID}
	ctx, stop := context.WithCancel(h.ctx)
	remote := &Remote{
		Device:   device,
		Fixed:    fixed,
		Agent:    agent,
		Reader:   history.Reader{Store: h.store, Recent: recent, Device: device.ID},
		db:       h.store.DB(),
		watched:  watched,
		stop:     stop,
		recorded: make(chan struct{}),
	}
	recorder := &history.Recorder{Store: h.store, Recent: recent, Device: device.ID, Collector: watched, MaxEntries: h.historyEntries}
	h.recording.Go(func() {
		defer close(remote.recorded)
		recorder.Run(ctx)
	})
	h.remotes = append(h.remotes, remote)
	slog.Info("collecting from another device", "name", device.Name, "address", device.Address)
}

func (h *Hub) find(id string) *Remote {
	for _, r := range h.remotes {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func (h *Hub) saved(ctx context.Context) ([]Device, error) {
	rows, err := h.store.DB().QueryContext(ctx, `SELECT id, name, address FROM hub_devices ORDER BY added, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var devices []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.Address); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}
