package metrics

// Battery is the charge of the machine's batteries and whether it runs on
// mains power. Machines without a battery report none.
type Battery struct {
	Percent   float64 `json:"percent"`
	PluggedIn bool    `json:"pluggedIn"`
}

// supplyReading is what Linux reports about one battery: its charge in
// percent and its status, such as "Charging" or "Discharging".
type supplyReading struct {
	percent float64
	status  string
}

// combineBatteries turns the readings of every battery into one, as Windows
// does: the average charge, plugged in unless a battery is discharging.
// Machines with two batteries are rare, mostly older laptops. It returns
// nil without readings.
func combineBatteries(readings []supplyReading) *Battery {
	if len(readings) == 0 {
		return nil
	}
	b := &Battery{PluggedIn: true}
	for _, r := range readings {
		b.Percent += r.percent
		if r.status == "Discharging" {
			b.PluggedIn = false
		}
	}
	b.Percent /= float64(len(readings))
	return b
}
