// Package update tells whether a newer release of usage-control exists. It
// asks GitHub's public releases API once a day, without an account, and only
// reads the answer; it never downloads or installs anything.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const (
	// latestURL answers with the newest release that is not a pre-release.
	latestURL = "https://api.github.com/repos/Chriszly/usage-control/releases/latest"
	// releaseURL is the page of one release, for its tag.
	releaseURL = "https://github.com/Chriszly/usage-control/releases/tag/"
	// Interval is how often GitHub is asked.
	Interval = 24 * time.Hour
	// maxResponseBytes is far more than a release's description needs.
	maxResponseBytes = 1 << 20
)

// release matches a release version, such as 1.2.3 or the tag v1.2.3.
var release = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// Status is what the page shows about updates.
type Status struct {
	// Current is the running version.
	Current string `json:"current"`
	// Latest is the newest release, only when it is newer than Current.
	Latest string `json:"latest,omitempty"`
	// URL is the page of that release, with what changed.
	URL string `json:"url,omitempty"`
}

// Checker asks GitHub for the newest release every Interval.
type Checker struct {
	current string
	client  *http.Client
	url     string

	// mu guards latest.
	mu     sync.Mutex
	latest string
}

// NewChecker returns a Checker for the running version current, or nil when
// current is not a release, such as a build of main, which has no release to
// compare with.
func NewChecker(current string) *Checker {
	if !release.MatchString(current) {
		return nil
	}
	return &Checker{
		current: current,
		client:  &http.Client{Timeout: 10 * time.Second},
		url:     latestURL,
	}
}

// Run checks now and then every Interval until ctx is done. A failed check
// is logged and tried again at the next one.
func (c *Checker) Run(ctx context.Context) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		if err := c.check(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("could not check for a newer version; set UPDATE_CHECK=false to stop checking", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Status returns the running version and, when there is one, the newer
// release.
func (c *Checker) Status() Status {
	c.mu.Lock()
	latest := c.latest
	c.mu.Unlock()

	status := Status{Current: c.current}
	if latest != "" && newer(latest, c.current) {
		status.Latest = latest
		status.URL = releaseURL + "v" + latest
	}
	return status
}

// check asks GitHub for the newest release.
func (c *Checker) check(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "usage-control/"+c.current)
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		// No release has been published yet.
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %s", response.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&body); err != nil {
		return fmt.Errorf("read GitHub's answer: %w", err)
	}
	match := release.FindStringSubmatch(body.TagName)
	if match == nil {
		return fmt.Errorf("the newest release is %q, which is not a version such as v1.2.3", body.TagName)
	}
	latest := match[1] + "." + match[2] + "." + match[3]

	c.mu.Lock()
	c.latest = latest
	c.mu.Unlock()
	return nil
}

// newer reports whether version a is newer than b; both are releases.
func newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

// parts returns the three numbers of a release version.
func parts(version string) [3]int {
	var p [3]int
	match := release.FindStringSubmatch(version)
	if match == nil {
		return p
	}
	for i := range p {
		p[i], _ = strconv.Atoi(match[i+1])
	}
	return p
}
