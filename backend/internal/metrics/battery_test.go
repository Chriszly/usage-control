package metrics

import (
	"reflect"
	"testing"
)

func TestCombineBatteries(t *testing.T) {
	tests := []struct {
		name     string
		readings []supplyReading
		want     *Battery
	}{
		{"no battery", nil, nil},
		{"charging", []supplyReading{{percent: 64, status: "Charging"}}, &Battery{Percent: 64, PluggedIn: true}},
		{"full", []supplyReading{{percent: 100, status: "Full"}}, &Battery{Percent: 100, PluggedIn: true}},
		{
			"two batteries, one discharging",
			[]supplyReading{{percent: 80, status: "Not charging"}, {percent: 40, status: "Discharging"}},
			&Battery{Percent: 60, PluggedIn: false},
		},
	}
	for _, tt := range tests {
		if got := combineBatteries(tt.readings); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: combineBatteries() = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}
