package metrics

import (
	"context"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Chriszly/usage-control/backend/internal/pdh"
)

// gpuReader reads every GPU through the performance counters Windows keeps for
// Task Manager, which work for any vendor: "GPU Engine" for the usage and
// "GPU Adapter Memory" for the GPU's own memory. Windows has no vendor-neutral
// GPU temperature; that of NVIDIA GPUs comes from nvidia-smi, which the NVIDIA
// driver installs.
type gpuReader struct {
	nvidia *nvidiaSMI

	// mu guards the query, which measures usage since its previous reading.
	mu      sync.Mutex
	query   *pdh.Query
	engines pdh.Counter
	// memory is 0 when the machine has no memory counter.
	memory pdh.Counter
	// adapters is what the registry tells about each GPU, read when the
	// query is opened and again only when the counters name an unknown GPU.
	adapters map[luid]adapter
	// err is why the counters could not be opened; then no GPU is read.
	err error
}

func newGPUReader() *gpuReader {
	r := &gpuReader{nvidia: newNvidiaSMI()}
	r.err = r.open()
	return r
}

// open opens the query with both counters.
func (r *gpuReader) open() error {
	query, err := pdh.Open()
	if err != nil {
		return err
	}
	r.query = query
	if r.engines, err = query.Add(`\GPU Engine(*)\Utilization Percentage`); err != nil {
		return err
	}
	// Without the memory counter, the GPUs are shown without memory.
	r.memory, _ = query.Add(`\GPU Adapter Memory(*)\Dedicated Usage`)
	// Usage is measured between two readings, so the first one starts it.
	// A failed start shows as a missing GPU.
	_ = query.Collect()
	r.adapters = adapters()
	return nil
}

func (r *gpuReader) read(context.Context) []GPU {
	if r.err != nil {
		return []GPU{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.query.Collect(); err != nil {
		return []GPU{}
	}
	engines, err := r.engines.Values()
	if err != nil {
		return []GPU{}
	}
	var memory map[string]float64
	if r.memory != 0 {
		memory, _ = r.memory.Values()
	}
	if !allKnown(engines, r.adapters) {
		r.adapters = adapters()
	}
	return gpusFromCounters(engines, memory, r.adapters)
}

// temperatures returns the temperature of each NVIDIA GPU, which the
// performance counters leave out.
func (r *gpuReader) temperatures(ctx context.Context) []Temperature {
	return r.nvidia.temperatures(ctx)
}

// hideWindow starts a program without a console window, which would flash up
// every few seconds when usage-control runs without one.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
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
