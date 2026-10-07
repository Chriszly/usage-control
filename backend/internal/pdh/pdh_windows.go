package pdh

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	dll                         = windows.NewLazySystemDLL("pdh.dll")
	pdhOpenQuery                = dll.NewProc("PdhOpenQueryW")
	pdhAddEnglishCounter        = dll.NewProc("PdhAddEnglishCounterW")
	pdhCollectQueryData         = dll.NewProc("PdhCollectQueryData")
	pdhGetFormattedCounterArray = dll.NewProc("PdhGetFormattedCounterArrayW")
)

const (
	formatDouble = 0x00000200
	moreData     = 0x800007D2
)

// counterValueItem is a PDH_FMT_COUNTERVALUE_ITEM_W holding a double.
type counterValueItem struct {
	name   *uint16
	status uint32
	value  float64
}

// Query is a set of counters read together. Counters that measure a rate,
// such as a usage, are measured between two calls to Collect.
type Query struct {
	handle uintptr
}

// Counter is one counter of a Query, with a value per instance.
type Counter uintptr

// Open opens an empty query.
//
//nolint:gosec // pdh.dll takes pointers, which need unsafe.
func Open() (*Query, error) {
	q := &Query{}
	if ret, _, _ := pdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&q.handle))); ret != 0 {
		return nil, fmt.Errorf("PdhOpenQuery: 0x%x", ret)
	}
	return q, nil
}

// Add adds a counter by its English path, such as
// `\GPU Engine(*)\Utilization Percentage`, which works in every language of
// Windows. It fails when the machine does not have the counter.
//
//nolint:gosec // pdh.dll takes pointers, which need unsafe.
func (q *Query) Add(path string) (Counter, error) {
	var counter Counter
	if ret, _, _ := pdhAddEnglishCounter.Call(q.handle, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(path))), 0, uintptr(unsafe.Pointer(&counter))); ret != 0 {
		return 0, fmt.Errorf("add counter %s: 0x%x", path, ret)
	}
	return counter, nil
}

// Collect reads the current values of all the query's counters.
func (q *Query) Collect() error {
	if ret, _, _ := pdhCollectQueryData.Call(q.handle); ret != 0 {
		return fmt.Errorf("PdhCollectQueryData: 0x%x", ret)
	}
	return nil
}

// Values returns the value of each instance of a counter at the last Collect,
// by instance name. Instances without a valid value, such as a process that
// just started, are left out.
//
//nolint:gosec // pdh.dll takes pointers, which need unsafe.
func (c Counter) Values() (map[string]float64, error) {
	var size, count uint32
	ret, _, _ := pdhGetFormattedCounterArray.Call(uintptr(c), uintptr(formatDouble), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if ret != moreData {
		return nil, fmt.Errorf("PdhGetFormattedCounterArray: 0x%x", ret)
	}
	// The buffer holds the items followed by their names, so it is larger
	// than count items; it is allocated as items so it is aligned for them.
	itemSize := uint32(unsafe.Sizeof(counterValueItem{}))
	buffer := make([]counterValueItem, (size+itemSize-1)/itemSize)
	ret, _, _ = pdhGetFormattedCounterArray.Call(uintptr(c), uintptr(formatDouble), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buffer[0])))
	if ret != 0 {
		return nil, fmt.Errorf("PdhGetFormattedCounterArray: 0x%x", ret)
	}
	values := make(map[string]float64, count)
	for _, item := range buffer[:count] {
		// PDH_CSTATUS_VALID_DATA and PDH_CSTATUS_NEW_DATA
		if item.status == 0 || item.status == 1 {
			values[windows.UTF16PtrToString(item.name)] = item.value
		}
	}
	return values, nil
}
