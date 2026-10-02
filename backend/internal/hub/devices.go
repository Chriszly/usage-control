// Package hub collects the usage of other devices on the local network, each
// of which runs usage-control, so one device can show them all.
package hub

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"

	"github.com/Chriszly/usage-control/backend/internal/history"
)

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
// as "Living room Pi=192.168.1.20:8080". The name and "=" may be left out;
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
	name, address = strings.TrimSpace(name), strings.TrimSpace(address)
	if !named {
		name, address = entry, entry
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return Device{}, fmt.Errorf("%q: write the address as host:port, such as 192.168.1.20:8080", entry)
	}
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return Device{}, fmt.Errorf("%q: the port must be a number from 1 to 65535", entry)
	}

	if name == "" || len(name) > maxNameLength {
		return Device{}, fmt.Errorf("%q: the name must have 1 to %d characters", entry, maxNameLength)
	}
	id := idOf(name)
	if id == "" {
		return Device{}, fmt.Errorf("%q: the name needs at least one letter or digit", entry)
	}
	if id == LocalID {
		return Device{}, fmt.Errorf("%q: the name %q is reserved for this device; pick another one", entry, name)
	}
	return Device{ID: id, Name: name, Address: address}, nil
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
