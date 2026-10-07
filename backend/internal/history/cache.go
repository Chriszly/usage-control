package history

import (
	"sync"
	"time"
)

// cacheFor is how long an answer from the database is served again at most.
// The page asks for a long range once a minute or less often, so every tab
// and every viewer of the hub in that minute shares one query.
const cacheFor = time.Minute

// rangeCache keeps the last answer of Store.Range per device and step. It is
// served again while from and to fall in the same steps as before and it is
// at most cacheFor old: within a step of a minute or more, only the newest
// step could have changed, by the values of under a minute.
type rangeCache struct {
	// now is time.Now, replaced in tests.
	now func() time.Time

	mu      sync.Mutex
	answers map[rangeKey]rangeAnswer
	// forgotten counts the calls of forget, so an answer read from the
	// database before one is not kept after it.
	forgotten uint64
}

type rangeKey struct {
	device string
	step   time.Duration
}

type rangeAnswer struct {
	// from and to are the steps the range's ends fell in, counted from the
	// Unix epoch.
	from, to int64
	at       time.Time
	series   []Series
}

func newRangeCache() rangeCache {
	return rangeCache{now: time.Now, answers: map[rangeKey]rangeAnswer{}}
}

// get returns the answer kept for the range, if it still serves. When it
// does not, it returns the count of forget calls so far, for put.
func (c *rangeCache) get(device string, from, to time.Time, step time.Duration) ([]Series, bool, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	answer, ok := c.answers[rangeKey{device, step}]
	if !ok || answer.from != stepOf(from, step) || answer.to != stepOf(to, step) || c.now().Sub(answer.at) >= cacheFor {
		return nil, false, c.forgotten
	}
	return answer.series, true, c.forgotten
}

// put keeps the answer for the range and forgets the answers too old to
// serve, so the cache holds at most one answer per device and step in use.
// forgotten is what get returned before the answer was read: when forget
// was called since, the answer may have values deleted meanwhile, and is not
// kept.
func (c *rangeCache) put(device string, from, to time.Time, step time.Duration, series []Series, forgotten uint64) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if forgotten != c.forgotten {
		return
	}
	for key, answer := range c.answers {
		if now.Sub(answer.at) >= cacheFor {
			delete(c.answers, key)
		}
	}
	c.answers[rangeKey{device, step}] = rangeAnswer{from: stepOf(from, step), to: stepOf(to, step), at: now, series: series}
}

// forget drops the answers of a device, after its history was deleted.
func (c *rangeCache) forget(device string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forgotten++
	for key := range c.answers {
		if key.device == device {
			delete(c.answers, key)
		}
	}
}

// stepOf returns which step since the Unix epoch t falls in, as Store.Range
// groups the values.
func stepOf(t time.Time, step time.Duration) int64 {
	return t.Unix() / max(1, int64(step/time.Second))
}
