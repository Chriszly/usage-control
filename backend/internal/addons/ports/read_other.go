//go:build !linux && !windows

package ports

import "context"

// Read returns nothing where the add-on does not read ports yet, such as
// macOS.
func Read(context.Context, string) (ports []Port, ok bool) {
	return nil, false
}
