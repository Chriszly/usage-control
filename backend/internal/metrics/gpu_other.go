//go:build !linux && !windows

package metrics

import "context"

// gpuReader reads no GPU: macOS reports GPU usage only through private
// interfaces.
type gpuReader struct{}

func newGPUReader() *gpuReader { return &gpuReader{} }

func (*gpuReader) read(context.Context) []GPU { return []GPU{} }

func (*gpuReader) temperatures(context.Context) []Temperature { return nil }
