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
		{
			"two batteries with power and health",
			[]supplyReading{
				{percent: 80, status: "Discharging", watts: 5, hasWatts: true, health: 90, hasHealth: true},
				{percent: 40, status: "Discharging", watts: 3, hasWatts: true, health: 70, hasHealth: true},
			},
			&Battery{Percent: 60, Watts: ptr(8.0), HealthPercent: ptr(80.0)},
		},
	}
	for _, tt := range tests {
		if got := combineBatteries(tt.readings); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: combineBatteries() = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func ptr(v float64) *float64 { return &v }
