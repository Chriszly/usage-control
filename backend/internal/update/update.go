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
	// RetryInterval is how soon a failed check is tried again, so a network
	// that was down for a moment does not delay the notice by a day.
	RetryInterval = time.Hour
	// maxResponseBytes is far more than a release's description needs.
	maxResponseBytes = 1 << 20
)

// release matches a release version, such as 1.2.3 or the tag v1.2.3. A
// bugfix release adds letters: 1.2.3a, 1.2.3b, ... 1.2.3z, 1.2.3aa.
var release = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)([a-z]*)$`)

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

	// mu guards latest and tag.
	mu     sync.Mutex
	latest string
	// tag is latest as the release names it, with or without the v.
	tag string
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
// is logged and tried again after RetryInterval.
func (c *Checker) Run(ctx context.Context) {
	for {
		err := c.check(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Warn("could not check for a newer version; trying again in an hour. Set UPDATE_CHECK=false to stop checking", "error", err)
		}
		timer := time.NewTimer(nextCheck(err))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// nextCheck is how long to wait after a check that ended with err.
func nextCheck(err error) time.Duration {
	if err != nil {
		return RetryInterval
	}
	return Interval
}

// Status returns the running version and, when there is one, the newer
// release.
func (c *Checker) Status() Status {
	c.mu.Lock()
	latest, tag := c.latest, c.tag
	c.mu.Unlock()

	status := Status{Current: c.current}
	if latest != "" && newer(latest, c.current) {
		status.Latest = latest
		status.URL = releaseURL + tag
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
	latest := match[1] + "." + match[2] + "." + match[3] + match[4]

	c.mu.Lock()
	// The tag matched release, so it is only digits, dots, letters and a v.
	c.latest, c.tag = latest, body.TagName
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

// parts returns the three numbers of a release version and the number of its
// bugfix letters: none is 0, a is 1, z is 26 and aa is 27.
func parts(version string) [4]int {
	var p [4]int
	match := release.FindStringSubmatch(version)
	if match == nil {
		return p
	}
	for i := range 3 {
		p[i], _ = strconv.Atoi(match[i+1])
	}
	for _, letter := range match[4] {
		p[3] = p[3]*26 + int(letter-'a') + 1
	}
	return p
}
