package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func checkerAgainst(t *testing.T, current string, handler http.HandlerFunc) *Checker {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := NewChecker(current)
	if c == nil {
		t.Fatalf("NewChecker(%q) = nil, want a checker", current)
	}
	c.url = server.URL
	return c
}

func TestStatusNamesANewerRelease(t *testing.T) {
	c := checkerAgainst(t, "0.1.0", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "usage-control/0.1.0" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte(`{"tag_name": "v0.10.0", "html_url": "https://example.com/elsewhere"}`))
	})
	if err := c.check(context.Background()); err != nil {
		t.Fatalf("check() error = %v", err)
	}

	want := Status{Current: "0.1.0", Latest: "0.10.0", URL: "https://github.com/Chriszly/usage-control/releases/tag/v0.10.0"}
	if got := c.Status(); got != want {
		t.Errorf("Status() = %+v, want %+v", got, want)
	}
}

func TestStatusLinksATagWithoutV(t *testing.T) {
	c := checkerAgainst(t, "0.1.0", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "0.2.0"}`))
	})
	if err := c.check(context.Background()); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	if got := c.Status().URL; got != "https://github.com/Chriszly/usage-control/releases/tag/0.2.0" {
		t.Errorf("Status().URL = %q, want the release page of tag 0.2.0", got)
	}
}

func TestStatusLeavesOutTheSameOrAnOlderRelease(t *testing.T) {
	for _, tag := range []string{"v1.2.3", "v1.2.2", "v0.9.9"} {
		c := checkerAgainst(t, "1.2.3", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name": "` + tag + `"}`))
		})
		if err := c.check(context.Background()); err != nil {
			t.Fatalf("check() error = %v", err)
		}
		if got := c.Status(); got != (Status{Current: "1.2.3"}) {
			t.Errorf("with %s: Status() = %+v, want only the current version", tag, got)
		}
	}
}

func TestCheckWithoutAnyReleaseIsNoError(t *testing.T) {
	c := checkerAgainst(t, "0.1.0", func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	})
	if err := c.check(context.Background()); err != nil {
		t.Errorf("check() error = %v, want none", err)
	}
	if got := c.Status(); got.Latest != "" {
		t.Errorf("Status().Latest = %q, want none", got.Latest)
	}
}

func TestCheckRefusesATagThatIsNotAVersion(t *testing.T) {
	c := checkerAgainst(t, "0.1.0", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v9.9.9\"><script>"}`))
	})
	if err := c.check(context.Background()); err == nil {
		t.Error("check() error = nil, want one for the odd tag")
	}
	if got := c.Status(); got.Latest != "" {
		t.Errorf("Status().Latest = %q, want none", got.Latest)
	}
}

func TestAFailedCheckIsTriedAgainSooner(t *testing.T) {
	if got := nextCheck(nil); got != Interval {
		t.Errorf("after a check: next in %v, want %v", got, Interval)
	}
	if got := nextCheck(errors.New("no network")); got != RetryInterval {
		t.Errorf("after a failed check: next in %v, want %v", got, RetryInterval)
	}
}

func TestNewCheckerSkipsBuildsThatAreNotReleases(t *testing.T) {
	for _, current := range []string{"dev", "main-1a2b3c4", "1.2", "1.2.3a-rc1", "1.2.3-RC1", "1.2.3-", "1.2.3-alpha.", "1.2.3A", ""} {
		if c := NewChecker(current); c != nil {
			t.Errorf("NewChecker(%q) = %v, want nil", current, c)
		}
	}
}

func TestNewerOrdersBugfixLetters(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"1.0.4a", "1.0.4", true},
		{"1.0.4b", "1.0.4a", true},
		{"1.0.4aa", "1.0.4z", true},
		{"1.0.5", "1.0.4z", true},
		{"1.0.4", "1.0.4a", false},
		{"1.0.4h", "1.0.4h", false},
		{"1.0.4z", "1.0.5", false},
	} {
		if got := newer(tc.a, tc.b); got != tc.want {
			t.Errorf("newer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestNewCheckerAcceptsAPreRelease(t *testing.T) {
	for _, current := range []string{"2.0.0-alpha", "2.0.0-rc1", "2.0.0-beta.2"} {
		if NewChecker(current) == nil {
			t.Errorf("NewChecker(%q) = nil, want a Checker", current)
		}
	}
}

func TestNewerOrdersPreReleases(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"2.0.0", "2.0.0-alpha", true},
		{"2.0.0-alpha", "1.3.1m", true},
		{"2.0.0-beta", "2.0.0-alpha", true},
		{"2.0.0-alpha.2", "2.0.0-alpha", true},
		{"2.0.0-alpha10", "2.0.0-alpha9", true},
		{"2.0.0-rc1", "2.0.0-beta.3", true},
		{"2.0.0-alpha", "2.0.0", false},
		{"2.0.0-alpha", "2.0.0-alpha", false},
		{"2.0.0-alpha", "2.0.0-beta", false},
	} {
		if got := newer(tc.a, tc.b); got != tc.want {
			t.Errorf("newer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestStatusNamesTheReleaseAfterAPreRelease(t *testing.T) {
	c := checkerAgainst(t, "2.0.0-alpha", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v2.0.0"}`))
	})
	if err := c.check(context.Background()); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	want := Status{Current: "2.0.0-alpha", Latest: "2.0.0", URL: "https://github.com/Chriszly/usage-control/releases/tag/v2.0.0"}
	if got := c.Status(); got != want {
		t.Errorf("Status() = %+v, want %+v", got, want)
	}
}

func TestStatusNamesANewerBugfixRelease(t *testing.T) {
	c := checkerAgainst(t, "1.0.8", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "1.0.8h"}`))
	})
	if err := c.check(context.Background()); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	want := Status{Current: "1.0.8", Latest: "1.0.8h", URL: "https://github.com/Chriszly/usage-control/releases/tag/1.0.8h"}
	if got := c.Status(); got != want {
		t.Errorf("Status() = %+v, want %+v", got, want)
	}
}
