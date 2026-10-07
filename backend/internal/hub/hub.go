package hub

import (
	"context"
	"database/sql"
	"errors"
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
	// pagePort is the port the hub's page is reachable on, which every
	// device is told; see PagePortHeader.
	pagePort string
	// suggester works out which device the page offers to add.
	suggester suggester
	// ctx ends every recorder when the program stops.
	ctx       context.Context
	recording sync.WaitGroup

	// beforeDelete, when set, is called before a removed device's data is
	// deleted, for tests.
	beforeDelete func(id string)

	// mu guards remotes and removing.
	mu      sync.Mutex
	remotes []*Remote
	// removing has a channel for each removed device whose recorder may still
	// run or whose data is still being deleted, closed once that is done.
	removing map[string]chan struct{}
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

	// mu guards kind, which the page can change.
	mu   sync.Mutex
	kind Kind
}

// New starts collecting from the fixed devices and the ones added on the page
// earlier, until ctx is done. The history is kept in store, with the first
// historyEntries disks, sensors, network cards and GPUs each of a device.
// Every device is told pagePort, the port the hub's page is reachable on.
func New(ctx context.Context, store *history.Store, fixed []Device, historyEntries int, pagePort string) (*Hub, error) {
	h := &Hub{store: store, historyEntries: historyEntries, pagePort: pagePort, suggester: defaultSuggester(), ctx: ctx}
	for _, schema := range []string{savedSchema, availabilitySchema, kindSchema} {
		if _, err := store.DB().ExecContext(ctx, schema); err != nil {
			return nil, err
		}
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
		// Devices added before each address and port could be added only once.
		if other := h.findAddress(device.Address); other != nil {
			slog.Warn("a device added on the page has the same address and port as another; collecting from it only once", "name", device.Name, "other", other.Name, "address", device.Address)
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
// usage-control answers at its address, keeps it in the database with its
// kind and starts collecting from it. Problems with the device are
// InputErrors.
func (h *Hub) Add(ctx context.Context, name, address string, kind Kind) (Device, error) {
	device, err := NewDevice(name, address)
	if err != nil {
		return Device{}, err
	}
	kind, err = ParseKind(string(kind))
	if err != nil {
		return Device{}, err
	}
	// A device with the same name that was just removed may still have its
	// data deleted, which would delete this one's too. A short delete is
	// waited for; a long one is not, so the request does not time out.
	if err := h.waitRemoved(ctx, device.ID, removingWait); err != nil {
		return Device{}, err
	}
	// Checked before asking the device, so a device added already is refused
	// at once, and again below, in case another change came in meanwhile.
	h.mu.Lock()
	err = h.taken(device)
	h.mu.Unlock()
	if err != nil {
		return Device{}, err
	}

	// Asked before taking mu, so the page is not held up while the device answers.
	if _, err := askOnce(ctx, device.Address); err != nil {
		return Device{}, &InputError{Problem: ProblemUnreachable, Message: fmt.Sprintf("no usage-control answers at %s: %v", device.Address, err)}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.stopping(); err != nil {
		return Device{}, err
	}
	if err := h.taken(device); err != nil {
		return Device{}, err
	}
	// The device and its kind are saved together, and even when the page that
	// asked has gone away, so a device is never saved without being collected.
	if err := h.save(context.WithoutCancel(ctx), device, kind); err != nil {
		return Device{}, err
	}
	h.start(device, false)
	return device, nil
}

// save keeps an added device and its kind in the database, both or neither.
func (h *Hub) save(ctx context.Context, device Device, kind Kind) error {
	tx, err := h.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO hub_devices (id, name, address, added) VALUES (?, ?, ?, ?)`,
		device.ID, device.Name, device.Address, time.Now().Unix())
	if err != nil {
		return err
	}
	if err := storeKind(ctx, tx, device.ID, kind); err != nil {
		return err
	}
	return tx.Commit()
}

// SetKind changes what a device is used as, a device from HUB_DEVICES too.
// The times it did not answer stay as they are; only what they mean changes.
func (h *Hub) SetKind(ctx context.Context, id string, kind Kind) error {
	kind, err := ParseKind(string(kind))
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	remote := h.find(id)
	if remote == nil {
		return &InputError{Problem: ProblemNotFound, Message: "there is no device with this id"}
	}
	if err := storeKind(ctx, h.store.DB(), id, kind); err != nil {
		return err
	}
	remote.mu.Lock()
	remote.kind = kind
	remote.mu.Unlock()
	slog.Info("changed the kind of another device", "name", remote.Name, "kind", kind)
	return nil
}

// Remove stops collecting from a device added on the page and forgets it.
// Its history and availability are deleted too, unless keepHistory is set;
// adding a device with the same name later continues them.
//
// Only taking the device off the list waits for mu; its recorder is stopped
// and its data deleted afterwards, in the background, as deleting a long
// history takes a while and every page request needs mu. Adding a device
// with the same name waits until that is done.
func (h *Hub) Remove(ctx context.Context, id string, keepHistory bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.stopping(); err != nil {
		return err
	}
	remote := h.find(id)
	if remote == nil {
		return &InputError{Problem: ProblemNotFound, Message: "there is no device with this id"}
	}
	if remote.Fixed {
		return &InputError{Problem: ProblemFixed, Message: "the device is set in HUB_DEVICES; remove it there"}
	}

	// Once the device is deleted from the database, the rest follows even
	// when the page that asked has gone away, so nothing is left half removed.
	if _, err := h.store.DB().ExecContext(context.WithoutCancel(ctx), `DELETE FROM hub_devices WHERE id = ?`, id); err != nil {
		return err
	}
	h.remotes = slices.DeleteFunc(h.remotes, func(r *Remote) bool { return r == remote })
	remote.stop()
	removed := make(chan struct{})
	if h.removing == nil {
		h.removing = map[string]chan struct{}{}
	}
	h.removing[id] = removed
	h.recording.Go(func() {
		defer func() {
			h.mu.Lock()
			delete(h.removing, id)
			h.mu.Unlock()
			close(removed)
		}()
		<-remote.recorded
		slog.Info("stopped collecting from another device", "name", remote.Name, "historyKept", keepHistory)
		if !keepHistory {
			h.deleteData(remote)
		}
	})
	return nil
}

// deleteData deletes the availability, kind and history of a removed device.
// When the program stops meanwhile, what is left of its history ages out
// with the retention.
func (h *Hub) deleteData(remote *Remote) {
	if h.beforeDelete != nil {
		h.beforeDelete(remote.ID)
	}
	err := forget(h.ctx, h.store.DB(), remote.ID)
	if err == nil {
		err = h.store.DeleteDevice(h.ctx, remote.ID)
	}
	switch {
	case err == nil:
		slog.Info("deleted the data of a removed device", "name", remote.Name)
	case h.ctx.Err() != nil:
		slog.Warn("stopped deleting the history of a removed device, as the program stops; the rest is deleted with the retention", "name", remote.Name)
	default:
		slog.Error("delete the data of a removed device; its history is deleted with the retention", "name", remote.Name, "error", err)
	}
}

// removingWait is how long adding a device waits for one with the same
// name to be removed, before it is refused with ProblemRemoving.
const removingWait = 2 * time.Second

// waitRemoved waits until the data of a removed device with id is deleted,
// if one is being removed, for at most limit. It returns errRemoving when
// that takes longer, or ctx's error when ctx is done first.
func (h *Hub) waitRemoved(ctx context.Context, id string, limit time.Duration) error {
	h.mu.Lock()
	removed := h.removing[id]
	h.mu.Unlock()
	if removed == nil {
		return nil
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-removed:
		return nil
	case <-timer.C:
		return errRemoving
	case <-ctx.Done():
		return ctx.Err()
	}
}

// errRemoving refuses a device whose name a device still being removed has.
var errRemoving = &InputError{Problem: ProblemRemoving, Message: "a device with the same name is still being removed; try again in a moment"}

// errStopping refuses changes once the program stops, so no recorder starts
// while the others are waited for and the database is closed.
var errStopping = errors.New("the hub is stopping")

// stopping returns errStopping once the ctx given to New is done. The caller
// holds mu.
func (h *Hub) stopping() error {
	if h.ctx.Err() != nil {
		return errStopping
	}
	return nil
}

// Unreachable reports whether the device has not answered recently, and since
// when it does not; since is zero when that is not known yet.
func (r *Remote) Unreachable() (since time.Time, unreachable bool) {
	if r.Agent.answers() {
		return time.Time{}, false
	}
	if outage, _, ok := r.watched.ongoing(); ok {
		return outage.Start, true
	}
	return time.Time{}, true
}

// Kind tells what the device is used as.
func (r *Remote) Kind() Kind {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.kind
}

// Availability tells how long the device did not answer since it was added.
func (r *Remote) Availability(ctx context.Context) (Availability, error) {
	availability, err := readAvailability(ctx, r.db, r.ID)
	if err != nil {
		return Availability{}, err
	}
	availability.Kind = r.Kind()
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
	agent.pagePort = h.pagePort
	agent.maxEntries = h.historyEntries
	if err := watch(h.ctx, h.store.DB(), device.ID, time.Now()); err != nil {
		slog.Error("store when the hub started collecting from a device", "name", device.Name, "error", err)
	}
	kind, err := readKind(h.ctx, h.store.DB(), device.ID)
	if err != nil {
		slog.Error("read the kind of a device; taking it for a server", "name", device.Name, "error", err)
		kind = KindServer
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
		kind:     kind,
	}
	fetcher := &fetcher{agent: agent, store: h.store, device: device.ID, maxValues: maxValues(h.historyEntries)}
	recorder := &history.Recorder{Store: h.store, Recent: recent, Device: device.ID, Collector: watched, MaxEntries: h.historyEntries, Fetch: fetcher.fetch}
	h.recording.Go(func() {
		defer close(remote.recorded)
		recorder.Run(ctx)
	})
	h.remotes = append(h.remotes, remote)
	slog.Info("collecting from another device", "name", device.Name, "address", device.Address)
}

// taken refuses a device whose name or whose address and port another
// device has. The caller holds mu.
func (h *Hub) taken(device Device) error {
	if h.find(device.ID) != nil {
		return &InputError{Problem: ProblemNameTaken, Message: "another device has the same name; give each device its own name"}
	}
	if h.removing[device.ID] != nil {
		return errRemoving
	}
	if other := h.findAddress(device.Address); other != nil {
		return &InputError{Problem: ProblemAddressTaken, Message: fmt.Sprintf("%s is already collected from at this address and port", other.Name)}
	}
	return nil
}

// findAddress returns the device at the same address and port. The caller
// holds mu.
func (h *Hub) findAddress(address string) *Remote {
	for _, r := range h.remotes {
		if SameAddress(r.Address, address) {
			return r
		}
	}
	return nil
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
