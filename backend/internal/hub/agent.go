package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"syscall"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/lan"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

const (
	// requestTimeout is how long a device has to answer.
	requestTimeout = 4 * time.Second
	// maxResponseBytes is the largest answer that is read. A snapshot is a
	// few kilobytes, and some 150 KB on a host with 64 disks, sensors and
	// network cards and 500 values of extras with three translations each.
	// Even every group and value of extras CleanExtras keeps, with labels of
	// the most characters, fits, as long as they are not all translated too:
	// a device that answers more counts as not answering, and the log says
	// why.
	maxResponseBytes = 1 << 20
	// drainBytes is how much of an answer is read after its JSON, so the
	// connection can be used again: a chunked answer, one larger than the
	// device's write buffer, ends after the JSON, which the decoder does not
	// read. More than that, the connection is closed instead.
	drainBytes = 4 << 10
	// staleAfter is how old the newest reading may be before the device
	// counts as unreachable: a few missed readings.
	staleAfter = 20 * time.Second
)

// PagePortHeader is sent with every request a hub makes, with the port its
// page is reachable on. The device combines it with the address the request
// came from, so its tray icon can link to the hub's page.
const PagePortHeader = "Usage-Control-Hub-Port"

// ErrUnreachable is returned when a device has not answered recently.
var ErrUnreachable = errors.New("the device has not answered recently")

// Agent reads the usage of a device from the usage-control running on it.
// The recorder calls Collect regularly; the page is served the newest reading
// from Latest, so the device is not asked once more for every open page.
type Agent struct {
	url string
	// minutesURL is where the device's minutes are fetched; see MinutesPath.
	minutesURL string
	client     *http.Client
	// pagePort is sent as PagePortHeader; empty sends none.
	pagePort string
	// hubID is sent as HubIDHeader; empty sends none.
	hubID string
	// maxEntries is how many groups of extras, and values in each, are kept
	// of an answer.
	maxEntries int
	// ownGeneration, when set, reads the hub's own addresses and returns how
	// often they gained one; see checkOwnGeneration and get. Set for a
	// device added on the page.
	ownGeneration func() uint64

	mu       sync.Mutex
	latest   metrics.Snapshot
	latestAt time.Time
	// offset is how far the hub's clock is ahead of the device's, as of the
	// newest reading.
	offset time.Duration
	// seenGeneration is the newest ownGeneration the connections kept idle
	// from before were closed for.
	seenGeneration uint64
}

// NewAgent returns an Agent for the device at address (host:port).
func NewAgent(address string) *Agent {
	return newAgent(address, localNetworkOnly)
}

// newAgent returns an Agent that checks every address it connects to with
// control, which refuses one by returning an error.
func newAgent(address string, control func(network, address string, c syscall.RawConn) error) *Agent {
	dialer := &net.Dialer{Timeout: requestTimeout, Control: control}
	return &Agent{
		url:        "http://" + address + "/api/metrics",
		minutesURL: "http://" + address + MinutesPath,
		maxEntries: history.DefaultMaxEntries,
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

// askOnce asks the device at address for its usage a single time and closes
// the connection, which would otherwise stay open unused.
func askOnce(ctx context.Context, address string) (metrics.Snapshot, error) {
	return askOnceWith(ctx, NewAgent(address))
}

func askOnceWith(ctx context.Context, agent *Agent) (metrics.Snapshot, error) {
	defer agent.client.CloseIdleConnections()
	return agent.ask(ctx)
}

// Collect asks the device for its current usage. The time of the snapshot is
// set to when it arrived, so a device whose clock is off is still recorded at
// the right time.
func (a *Agent) Collect(ctx context.Context) (metrics.Snapshot, error) {
	snapshot, err := a.ask(ctx)
	if err != nil {
		return snapshot, err
	}
	now := time.Now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.offset = 0
	if !snapshot.Time.IsZero() {
		a.offset = now.Sub(snapshot.Time).Round(time.Second)
	}
	snapshot.Time = now
	a.latest, a.latestAt = snapshot, now
	return snapshot, nil
}

// answers reports whether the device has answered recently, as
// LatestCollector serves it.
func (a *Agent) answers() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.fresh()
}

// fresh reports whether the newest reading is young enough to serve. The
// caller holds mu.
func (a *Agent) fresh() bool {
	return !a.latestAt.IsZero() && time.Since(a.latestAt) <= staleAfter
}

// ask asks the device for its current usage.
func (a *Agent) ask(ctx context.Context) (metrics.Snapshot, error) {
	var snapshot metrics.Snapshot
	if err := a.get(ctx, a.url, maxResponseBytes, &snapshot); err != nil {
		return metrics.Snapshot{}, err
	}
	// The extras are shown and stored as the device describes them, so they
	// are cut down to what is safe for that first.
	snapshot.Extras = metrics.CleanExtras(snapshot.Extras, a.maxEntries)
	return snapshot, nil
}

// get asks the device at url and reads its JSON answer, of at most limit
// bytes, into answer. An answer other than 200 OK is a *statusError.
func (a *Agent) get(ctx context.Context, url string, limit int64, answer any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if a.pagePort != "" {
		request.Header.Set(PagePortHeader, a.pagePort)
	}
	if a.hubID != "" {
		request.Header.Set(HubIDHeader, a.hubID)
	}
	if a.ownGeneration != nil {
		// A connection kept open is checked against the hub's own addresses
		// only when it was opened. Once they gained one, the connections
		// kept idle from before are closed before a request, so it connects
		// anew and is checked. One that was in use meanwhile and is kept
		// again is closed when it would be used again, before anything is
		// sent over it, and the request is sent over a new one.
		a.closeKeptBefore(a.ownGeneration())
		request = request.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) {
				// Only a reused connection is closed. A new one was just
				// checked when it was opened, even when its generation is
				// already behind, and closing it would fail the request:
				// the transport sends it again over another connection only
				// when a reused one fails, so the reading would count as an
				// outage.
				if conn, ok := info.Conn.(*generationConn); ok && info.Reused && conn.generation < a.ownGeneration() {
					_ = conn.Close()
				}
			},
		}))
	}
	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("ask %s: %w", url, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, drainBytes))
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return &statusError{URL: url, Status: response.Status, Code: response.StatusCode}
	}
	body := &io.LimitedReader{R: response.Body, N: limit}
	if err := json.NewDecoder(body).Decode(answer); err != nil {
		if body.N == 0 {
			return fmt.Errorf("read the answer of %s: it is larger than the %d KiB that are read", url, limit>>10)
		}
		return fmt.Errorf("read the answer of %s: %w", url, err)
	}
	return nil
}

// closeKeptBefore closes the connections kept idle from before generation
// of the hub's own addresses, unless they were already. The generation is
// recorded only once they are closed, so a request that starts meanwhile
// closes them too instead of using one.
func (a *Agent) closeKeptBefore(generation uint64) {
	a.mu.Lock()
	newer := generation > a.seenGeneration
	a.mu.Unlock()
	if !newer {
		return
	}
	a.client.CloseIdleConnections()
	a.mu.Lock()
	a.seenGeneration = max(a.seenGeneration, generation)
	a.mu.Unlock()
}

// checkOwnGeneration makes the agent stop using a connection it kept open
// once the hub's own addresses gained one since it was opened, with
// ownGeneration returning how often they did; see get.
func (a *Agent) checkOwnGeneration(ownGeneration func() uint64) {
	a.ownGeneration = ownGeneration
	transport := a.client.Transport.(*http.Transport)
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		// Read before connecting, so the addresses the connection is
		// checked against are at least as new.
		generation := ownGeneration()
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &generationConn{Conn: conn, generation: generation}, nil
	}
}

// generationConn is a connection of an agent of a device added on the
// page, opened when the hub's own addresses had gained one generation
// times.
type generationConn struct {
	net.Conn
	generation uint64
}

// statusError is a device's answer other than 200 OK.
type statusError struct {
	URL    string
	Status string
	Code   int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("ask %s: answered %s", e.URL, e.Status)
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
	if !l.agent.fresh() {
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

// clockOffset returns how far the hub's clock is ahead of the device's, or
// false when the device has not answered recently.
func (a *Agent) clockOffset() (time.Duration, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.offset, a.fresh()
}
