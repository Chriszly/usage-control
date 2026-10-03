package metrics

import (
	"reflect"
	"testing"
)

func TestParseThrottling(t *testing.T) {
	tests := []struct {
		text string
		want *Throttling
	}{
		{"0\n", &Throttling{Now: []string{}, SinceBoot: []string{}}},
		{"50005\n", &Throttling{
			Now:       []string{"undervoltage", "throttled"},
			SinceBoot: []string{"undervoltage", "throttled"},
		}},
		{"0x80000", &Throttling{Now: []string{}, SinceBoot: []string{"softTemperatureLimit"}}},
		{"not a number", nil},
	}
	for _, tt := range tests {
		if got := parseThrottling(tt.text); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseThrottling(%q) = %+v, want %+v", tt.text, got, tt.want)
		}
	}
}

func TestReadThrottlingWithoutFirmwareIsNil(t *testing.T) {
	if got := readThrottling(""); got != nil {
		t.Errorf("readThrottling(\"\") = %+v, want nil", got)
	}
}
