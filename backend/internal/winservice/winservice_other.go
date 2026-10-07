//go:build !windows

// Package winservice runs a program as a Windows service when the service
// manager starts it.
package winservice

import "context"

// Run reports false: only Windows runs a program as a service.
func Run(string, func(context.Context) error) (bool, error) {
	return false, nil
}

// LogWarnings does nothing: only Windows has an event log.
func LogWarnings(string) {}
