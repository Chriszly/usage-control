//go:build !linux && !windows

package ports

import (
	"context"
	"errors"
)

var errNotSupported = errors.New("reading the ports is not supported on this system yet")

// read fails where the add-on does not read ports yet, such as macOS.
func read(context.Context, string) ([]Port, error) {
	return nil, errNotSupported
}
