//go:build !windows

package main

import "context"

// runAsService reports false: only Windows runs the program as a service.
func runAsService(func(context.Context) error) (bool, error) {
	return false, nil
}
