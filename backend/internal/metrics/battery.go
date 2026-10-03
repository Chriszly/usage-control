package metrics

// Battery is the charge of the machine's batteries and whether it runs on
// mains power. Machines without a battery report none. Linux also reports
// the power flowing in or out (Watts) and how much the batteries hold
// compared to when they were new (HealthPercent).
type Battery struct {
	Percent       float64  `json:"percent"`
	PluggedIn     bool     `json:"pluggedIn"`
	Watts         *float64 `json:"watts,omitempty"`
	HealthPercent *float64 `json:"healthPercent,omitempty"`
}

// supplyReading is what Linux reports about one battery: its charge in
// percent, its status, such as "Charging" or "Discharging", and where the
// battery reports them, its power and health.
type supplyReading struct {
	percent float64
	status  string
	watts   *float64
	health  *float64
}

// combineBatteries turns the readings of every battery into one, as Windows
// does: the average charge, plugged in unless a battery is discharging.
// Their power is added up and their health averaged, when every battery
// reports it. Machines with two batteries are rare, mostly older laptops. It
// returns nil without readings.
func combineBatteries(readings []supplyReading) *Battery {
	if len(readings) == 0 {
		return nil
	}
	b := &Battery{PluggedIn: true, Watts: new(float64), HealthPercent: new(float64)}
	for _, r := range readings {
		b.Percent += r.percent
		if r.status == "Discharging" {
			b.PluggedIn = false
		}
		b.Watts = addKnown(b.Watts, r.watts, 1)
		b.HealthPercent = addKnown(b.HealthPercent, r.health, float64(len(readings)))
	}
	b.Percent /= float64(len(readings))
	return b
}

// addKnown adds value divided by n to total, or returns nil when either is unknown.
func addKnown(total, value *float64, n float64) *float64 {
	if total == nil || value == nil {
		return nil
	}
	sum := *total + *value/n
	return &sum
}
