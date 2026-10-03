// Package hub collects the usage of other devices on the local network, each
// of which runs usage-control, so one device can show them all.
package hub

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Chriszly/usage-control/backend/internal/history"
)

// addressPattern matches host:port, where the host is a name or IPv4 address
// (letters, digits, dots and dashes) or an IPv6 address in brackets, and
// captures the port.
var addressPattern = regexp.MustCompile(`^(?:[A-Za-z0-9.-]+|\[[0-9A-Fa-f:.]+\]):([0-9]{1,5})$`)

// maxNameLength is the longest device name that is accepted.
const maxNameLength = 64

// Device is another machine on the local network this one collects from.
type Device struct {
	// ID is the name in lower case with dashes instead of spaces, such as
	// "living-room-pi". The history is stored under it.
	ID string
	// Name is how the device is shown on the page.
	Name string
	// Address is where its usage-control is reachable, as host:port.
	Address string
}

// ParseDevices reads the devices from a comma-separated list of entries such
// as "Living room Pi=192.168.1.20:9393". The name and "=" may be left out;
// the device is then named after its address.
func ParseDevices(value string) ([]Device, error) {
	var devices []Device
	ids := map[string]bool{}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		device, err := parseDevice(entry)
		if err != nil {
			return nil, err
		}
		if ids[device.ID] {
			return nil, fmt.Errorf("%q: another device has the same name; give each device its own name", entry)
		}
		ids[device.ID] = true
		devices = append(devices, device)
	}
	return devices, nil
}

func parseDevice(entry string) (Device, error) {
	name, address, named := strings.Cut(entry, "=")
	if !named {
		name, address = entry, entry
	}
	device, err := NewDevice(name, address)
	if err != nil {
		return Device{}, fmt.Errorf("%q: %w", entry, err)
	}
	return device, nil
}

// NewDevice checks a device's name and address (host:port) and returns it
// with its ID. The errors are InputErrors.
func NewDevice(name, address string) (Device, error) {
	name, address = strings.TrimSpace(name), strings.TrimSpace(address)

	// The address becomes part of the URL the hub asks, so it may only hold a
	// host name or IP address and a port: no path, user or other URL parts.
	match := addressPattern.FindStringSubmatch(address)
	if match == nil {
		return Device{}, &InputError{Problem: ProblemAddress, Message: "write the address as host:port, such as 192.168.1.20:9393"}
	}
	if port, _ := strconv.Atoi(match[1]); port < 1 || port > 65535 {
		return Device{}, &InputError{Problem: ProblemAddress, Message: "the port must be a number from 1 to 65535"}
	}

	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return Device{}, &InputError{Problem: ProblemName, Message: fmt.Sprintf("the name must have 1 to %d characters", maxNameLength)}
	}
	id := idOf(name)
	if id == "" {
		return Device{}, &InputError{Problem: ProblemName, Message: "the name needs at least one letter or digit"}
	}
	if id == LocalID {
		return Device{}, &InputError{Problem: ProblemNameTaken, Message: fmt.Sprintf("the name %q is reserved for this device; pick another one", name)}
	}
	return Device{ID: id, Name: name, Address: address}, nil
}

// Problem names what is wrong with a device that cannot be added or
// removed, so the page can explain it in the visitor's language.
type Problem string

// The problems an InputError can have.
const (
	ProblemName        Problem = "name"
	ProblemNameTaken   Problem = "nameTaken"
	ProblemAddress     Problem = "address"
	ProblemUnreachable Problem = "unreachable"
	ProblemNotFound    Problem = "notFound"
	ProblemFixed       Problem = "fixed"
)

// InputError is a device that cannot be added or removed as asked.
type InputError struct {
	Problem Problem
	Message string
}

func (e *InputError) Error() string {
	return e.Message
}

// LocalID is the ID of the device the program runs on, the name its own
// history is stored under.
const LocalID = history.LocalDevice

// idOf turns a name into an ID: letters and digits in lower case, with one
// dash for every run of other characters, such as "living-room-pi".
func idOf(name string) string {
	var id strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			dash = id.Len() > 0
			continue
		}
		if dash {
			id.WriteRune('-')
			dash = false
		}
		id.WriteRune(r)
	}
	return id.String()
}
