package hub

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDevices(t *testing.T) {
	tests := []struct {
		value string
		want  []Device
	}{
		{"", nil},
		{" , ", nil},
		{
			"Living room Pi=192.168.1.20:9393, office.lan:9000, Pi on another port=192.168.1.20:9394",
			[]Device{
				{ID: "living-room-pi", Name: "Living room Pi", Address: "192.168.1.20:9393"},
				{ID: "office-lan-9000", Name: "office.lan:9000", Address: "office.lan:9000"},
				{ID: "pi-on-another-port", Name: "Pi on another port", Address: "192.168.1.20:9394"},
			},
		},
		{"Küche=[fd00::5]:9393", []Device{{ID: "küche", Name: "Küche", Address: "[fd00::5]:9393"}}},
	}
	for _, tt := range tests {
		got, err := ParseDevices(tt.value)
		if err != nil {
			t.Errorf("ParseDevices(%q) error = %v", tt.value, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseDevices(%q) = %+v, want %+v", tt.value, got, tt.want)
		}
	}
}

func TestParseDevicesRefusesInvalidEntries(t *testing.T) {
	for _, value := range []string{
		"Pi=192.168.1.20",       // no port
		"Pi=:9393",              // no host
		"Pi=192.168.1.20:0",     // port out of range
		"Pi=192.168.1.20:http",  // port not a number
		"=192.168.1.20:9393",    // empty name
		"--=192.168.1.20:9393",  // no letter or digit
		"Local=192.168.1.20:80", // the name of this device
		"Pi=10.0.0.1:80,pi=10.0.0.2:80",
		"Pi=10.0.0.1:80,NAS=10.0.0.1:80", // the same address and port twice
	} {
		if _, err := ParseDevices(value); err == nil {
			t.Errorf("ParseDevices(%q) error = nil, want an error", value)
		}
	}
}

func TestNewDeviceCountsTheNameLengthInCharacters(t *testing.T) {
	name := strings.Repeat("ü", maxNameLength) // twice as many bytes
	if _, err := NewDevice(name, "192.168.1.20:8080"); err != nil {
		t.Errorf("NewDevice(%d umlauts) error = %v, want it accepted", maxNameLength, err)
	}
	if _, err := NewDevice(name+"ü", "192.168.1.20:8080"); problemOf(err) != ProblemName {
		t.Errorf("NewDevice(%d umlauts) error = %v, want problem %q", maxNameLength+1, err, ProblemName)
	}
}

func TestNewDeviceRefusesURLParts(t *testing.T) {
	for _, address := range []string{
		"192.168.1.20:9393/admin",
		"user@192.168.1.20:9393",
		"http://192.168.1.20:9393",
		"192.168.1.20:9393?x=1",
		"192.168.1.20:9393#x",
	} {
		if _, err := NewDevice("Pi", address); err == nil {
			t.Errorf("NewDevice(%q) error = nil, want it refused", address)
		}
	}
}

func TestSameAddress(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"192.168.1.20:9393", "192.168.1.20:9393", true},
		{"192.168.1.20:9393", "192.168.1.20:09393", true},
		{"192.168.1.20:9393", "[::ffff:192.168.1.20]:9393", true},
		{"Office-PC.fritz.box:9393", "office-pc.fritz.box.:9393", true},
		{"[FD00::1]:9393", "[fd00::1]:9393", true},
		{"192.168.1.20:9393", "192.168.1.20:9394", false}, // another port is another device
		{"192.168.1.20:9393", "192.168.1.21:9393", false},
		{"office-pc:9393", "192.168.1.20:9393", false}, // names are not looked up
	}
	for _, tt := range tests {
		if got := SameAddress(tt.a, tt.b); got != tt.want {
			t.Errorf("SameAddress(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
