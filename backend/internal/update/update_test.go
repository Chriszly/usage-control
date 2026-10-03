package update

import (
	"context"
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

func TestNewCheckerSkipsBuildsThatAreNotReleases(t *testing.T) {
	for _, current := range []string{"dev", "main-1a2b3c4", "1.2", ""} {
		if c := NewChecker(current); c != nil {
			t.Errorf("NewChecker(%q) = %v, want nil", current, c)
		}
	}
}
