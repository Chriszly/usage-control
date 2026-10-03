package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/lan"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

const (
	// requestTimeout is how long a device has to answer.
	requestTimeout = 4 * time.Second
	// maxResponseBytes is the largest answer that is read; a snapshot is a
	// few kilobytes.
	maxResponseBytes = 1 << 20
	// staleAfter is how old the newest reading may be before the device
	// counts as unreachable: a few missed readings.
	staleAfter = 20 * time.Second
)

// ErrUnreachable is returned when a device has not answered recently.
var ErrUnreachable = errors.New("the device has not answered recently")

// Agent reads the usage of a device from the usage-control running on it.
// The recorder calls Collect regularly; the page is served the newest reading
// from Latest, so the device is not asked once more for every open page.
type Agent struct {
	url    string
	client *http.Client

	mu       sync.Mutex
	latest   metrics.Snapshot
	latestAt time.Time
	// failingSince is when asking the device started to fail; zero while it answers.
	failingSince time.Time
}

// NewAgent returns an Agent for the device at address (host:port).
func NewAgent(address string) *Agent {
	dialer := &net.Dialer{Timeout: requestTimeout, Control: localNetworkOnly}
	return &Agent{
		url: "http://" + address + "/api/metrics",
		client: &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				// No proxy: the device is on the local network.
				DialContext:         dialer.DialContext,
				MaxIdleConnsPerHost: 1,
			},
			// The API answers directly; a redirect could lead off the local network.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Collect asks the device for its current usage. The time of the snapshot is
// set to when it arrived, so a device whose clock is off is still recorded at
// the right time.
func (a *Agent) Collect(ctx context.Context) (metrics.Snapshot, error) {
	snapshot, err := a.ask(ctx)
	now := time.Now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		if a.failingSince.IsZero() {
			a.failingSince = now
		}
		return snapshot, err
	}
	snapshot.Time = now
	a.latest, a.latestAt, a.failingSince = snapshot, now, time.Time{}
	return snapshot, nil
}

// Unreachable reports whether the device has not answered recently, as
// LatestCollector does, and since when asking it fails; since is zero when it
// has not been asked yet.
func (a *Agent) Unreachable() (since time.Time, unreachable bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.latestAt.IsZero() && time.Since(a.latestAt) <= staleAfter {
		return time.Time{}, false
	}
	return a.failingSince, true
}

// ask asks the device for its current usage.
func (a *Agent) ask(ctx context.Context) (metrics.Snapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url, nil)
	if err != nil {
		return metrics.Snapshot{}, err
	}
	response, err := a.client.Do(request)
	if err != nil {
		return metrics.Snapshot{}, fmt.Errorf("ask %s: %w", a.url, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return metrics.Snapshot{}, fmt.Errorf("ask %s: answered %s", a.url, response.Status)
	}

	var snapshot metrics.Snapshot
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&snapshot); err != nil {
		return metrics.Snapshot{}, fmt.Errorf("read the answer of %s: %w", a.url, err)
	}
	return snapshot, nil
}

// Latest returns a collector that answers with the newest snapshot Collect
// read, or ErrUnreachable when it is older than staleAfter.
func (a *Agent) Latest() LatestCollector {
	return LatestCollector{agent: a}
}

// LatestCollector serves the newest snapshot of an Agent without asking the
// device again.
type LatestCollector struct {
	agent *Agent
}

// Collect returns the newest snapshot of the device.
func (l LatestCollector) Collect(context.Context) (metrics.Snapshot, error) {
	l.agent.mu.Lock()
	defer l.agent.mu.Unlock()
	if l.agent.latestAt.IsZero() || time.Since(l.agent.latestAt) > staleAfter {
		return metrics.Snapshot{}, ErrUnreachable
	}
	return l.agent.latest, nil
}

// localNetworkOnly refuses to connect to an address outside the local network,
// even when a host name resolves to one, so the hub only ever talks to its
// own network, as the website only answers it.
func localNetworkOnly(_, address string, _ syscall.RawConn) error {
	if !lan.Default.LocalAddrPort(address) {
		return fmt.Errorf("%s is not on the local network", address)
	}
	return nil
}
