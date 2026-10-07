package pressure

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

var (
	pdh                       = windows.NewLazySystemDLL("pdh.dll")
	pdhOpenQuery              = pdh.NewProc("PdhOpenQueryW")
	pdhAddEnglishCounter      = pdh.NewProc("PdhAddEnglishCounterW")
	pdhCollectQueryData       = pdh.NewProc("PdhCollectQueryData")
	pdhGetFormattedCounterVal = pdh.NewProc("PdhGetFormattedCounterValue")
)

// pdhFormatDouble asks PdhGetFormattedCounterValue for a double.
const pdhFormatDouble = 0x00000200

// pdhCounterValue is a PDH_FMT_COUNTERVALUE holding a double.
type pdhCounterValue struct {
	status uint32
	_      uint32 // the union that follows starts at 8 bytes
	value  float64
}

// The counters, by their English names, which work in every language of
// Windows.
const (
	processorQueuePath = `\System\Processor Queue Length`
	pagesInputPath     = `\Memory\Pages Input/sec`
	diskIdlePath       = `\PhysicalDisk(_Total)\% Idle Time`
)

// reader reads the performance counters through pdh.dll, which Windows
// carries; any account may read them.
type reader struct {
	mu                                   sync.Mutex
	query                                uintptr
	processorQueue, pagesInput, diskIdle uintptr
}

// NewReader returns what reads the closest signals to pressure that Windows
// offers (see counters). When the counters cannot be opened it logs why and
// reports nothing.
func NewReader() func() []metrics.Extra {
	r := &reader{}
	if err := r.open(); err != nil {
		slog.Error("the performance counters could not be opened", "error", err)
		return func() []metrics.Extra { return nil }
	}
	return r.read
}

// open opens the query with the counters. A counter that cannot be added,
// such as the disks' on a machine without the disk counters, is left out.
//
//nolint:gosec // pdh.dll takes pointers, which need unsafe.
func (r *reader) open() error {
	if ret, _, _ := pdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&r.query))); ret != 0 {
		return fmt.Errorf("PdhOpenQuery: 0x%x", ret)
	}
	added := 0
	for _, c := range []struct {
		path    string
		counter *uintptr
	}{
		{processorQueuePath, &r.processorQueue},
		{pagesInputPath, &r.pagesInput},
		{diskIdlePath, &r.diskIdle},
	} {
		ret, _, _ := pdhAddEnglishCounter.Call(r.query, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(c.path))), 0, uintptr(unsafe.Pointer(c.counter)))
		if ret != 0 {
			slog.Warn("a performance counter is missing", "counter", c.path, "error", fmt.Sprintf("0x%x", ret))
			*c.counter = 0
			continue
		}
		added++
	}
	if added == 0 {
		return errors.New("none of the counters could be added")
	}
	// Rates are measured between two readings, so the first one starts them.
	_, _, _ = pdhCollectQueryData.Call(r.query)
	return nil
}

func (r *reader) read() []metrics.Extra {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ret, _, _ := pdhCollectQueryData.Call(r.query); ret != 0 {
		return nil
	}
	return fromCounters(counters{
		processorQueue: counterValue(r.processorQueue),
		pagesInput:     counterValue(r.pagesInput),
		diskIdle:       counterValue(r.diskIdle),
	})
}

// counterValue returns the value of a counter, or nil when it has none, such
// as a counter that was not added.
//
//nolint:gosec // pdh.dll takes pointers, which need unsafe.
func counterValue(counter uintptr) *float64 {
	if counter == 0 {
		return nil
	}
	var value pdhCounterValue
	ret, _, _ := pdhGetFormattedCounterVal.Call(counter, uintptr(pdhFormatDouble), 0, uintptr(unsafe.Pointer(&value)))
	// PDH_CSTATUS_VALID_DATA and PDH_CSTATUS_NEW_DATA
	if ret != 0 || (value.status != 0 && value.status != 1) {
		return nil
	}
	return &value.value
}
