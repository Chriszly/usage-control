package metrics

import (
	"context"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// gpuReader reads every GPU through the performance counters Windows keeps for
// Task Manager, which work for any vendor: "GPU Engine" for the usage and
// "GPU Adapter Memory" for the GPU's own memory. Windows has no vendor-neutral
// GPU temperature, so it is left out.
type gpuReader struct {
	// mu guards the query, which measures usage since its previous reading.
	mu      sync.Mutex
	query   uintptr
	engines uintptr
	memory  uintptr
	// adapters is what the registry tells about each GPU, read when the
	// query is opened and again only when the counters name an unknown GPU.
	adapters map[luid]adapter
	// err is why the counters could not be opened; then no GPU is read.
	err error
}

var (
	pdh                         = windows.NewLazySystemDLL("pdh.dll")
	pdhOpenQuery                = pdh.NewProc("PdhOpenQueryW")
	pdhAddEnglishCounter        = pdh.NewProc("PdhAddEnglishCounterW")
	pdhCollectQueryData         = pdh.NewProc("PdhCollectQueryData")
	pdhGetFormattedCounterArray = pdh.NewProc("PdhGetFormattedCounterArrayW")
)

const (
	pdhFormatDouble = 0x00000200
	pdhMoreData     = 0x800007D2
)

// pdhCounterValueItem is a PDH_FMT_COUNTERVALUE_ITEM_W holding a double.
type pdhCounterValueItem struct {
	name   *uint16
	status uint32
	value  float64
}

func newGPUReader() *gpuReader {
	r := &gpuReader{}
	r.err = r.open()
	return r
}

// open opens the query with both counters.
//
//nolint:gosec // pdh.dll takes pointers, which need unsafe.
func (r *gpuReader) open() error {
	if ret, _, _ := pdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&r.query))); ret != 0 {
		return fmt.Errorf("PdhOpenQuery: 0x%x", ret)
	}
	engines := `\GPU Engine(*)\Utilization Percentage`
	if ret, _, _ := pdhAddEnglishCounter.Call(r.query, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(engines))), 0, uintptr(unsafe.Pointer(&r.engines))); ret != 0 {
		return fmt.Errorf("add counter %s: 0x%x", engines, ret)
	}
	// Without the memory counter, the GPUs are shown without memory.
	memory := `\GPU Adapter Memory(*)\Dedicated Usage`
	if ret, _, _ := pdhAddEnglishCounter.Call(r.query, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(memory))), 0, uintptr(unsafe.Pointer(&r.memory))); ret != 0 {
		r.memory = 0
	}
	// Usage is measured between two readings, so the first one starts it.
	// A failed start shows as a missing GPU.
	_, _, _ = pdhCollectQueryData.Call(r.query)
	r.adapters = adapters()
	return nil
}

func (r *gpuReader) read(context.Context) []GPU {
	if r.err != nil {
		return []GPU{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ret, _, _ := pdhCollectQueryData.Call(r.query); ret != 0 {
		return []GPU{}
	}
	engines, err := counterValues(r.engines)
	if err != nil {
		return []GPU{}
	}
	var memory map[string]float64
	if r.memory != 0 {
		memory, _ = counterValues(r.memory)
	}
	if !allKnown(engines, r.adapters) {
		r.adapters = adapters()
	}
	return gpusFromCounters(engines, memory, r.adapters)
}

// counterValues returns the value of each instance of a counter, by instance
// name. Instances without a valid value, such as a process that just
// started, are left out.
//
//nolint:gosec // pdh.dll takes pointers, which need unsafe.
func counterValues(counter uintptr) (map[string]float64, error) {
	var size, count uint32
	ret, _, _ := pdhGetFormattedCounterArray.Call(counter, uintptr(pdhFormatDouble), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if ret != pdhMoreData {
		return nil, fmt.Errorf("PdhGetFormattedCounterArray: 0x%x", ret)
	}
	// The buffer holds the items followed by their names, so it is larger
	// than count items; it is allocated as items so it is aligned for them.
	itemSize := uint32(unsafe.Sizeof(pdhCounterValueItem{}))
	buffer := make([]pdhCounterValueItem, (size+itemSize-1)/itemSize)
	ret, _, _ = pdhGetFormattedCounterArray.Call(counter, uintptr(pdhFormatDouble), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buffer[0])))
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

// adapters returns the name and memory size of each GPU, by its LUID. DirectX
// keeps them in the registry under HKLM\SOFTWARE\Microsoft\DirectX.
func adapters() map[luid]adapter {
	result := map[luid]adapter{}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\DirectX`, registry.READ)
	if err != nil {
		return result
	}
	defer func() { _ = key.Close() }()
	names, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return result
	}
	for _, name := range names {
		sub, err := registry.OpenKey(key, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		id, _, idErr := sub.GetIntegerValue("AdapterLuid")
		description, _, nameErr := sub.GetStringValue("Description")
		memory, _, _ := sub.GetIntegerValue("DedicatedVideoMemory")
		_ = sub.Close()
		if idErr == nil && nameErr == nil {
			result[luid(id)] = adapter{name: description, memoryBytes: memory}
		}
	}
	return result
}
