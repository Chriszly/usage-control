package history

import "github.com/Chriszly/usage-control/backend/internal/metrics"

// Metric names. Values that exist once per disk, sensor or network interface
// get its name after a colon, such as "disk:/" or "network.receive:eth0".
const (
	MetricCPU            = "cpu"
	MetricMemory         = "memory"
	MetricTemperature    = "temperature"
	MetricDisk           = "disk"
	MetricNetworkReceive = "network.receive"
	MetricNetworkSend    = "network.send"
)

// values returns the values of a snapshot that are kept in the history:
// usage in percent, temperatures in °C and network speeds in bytes per second.
func values(s metrics.Snapshot) map[string]float64 {
	v := map[string]float64{
		MetricCPU:    s.CPU.UsagePercent,
		MetricMemory: s.Memory.UsedPercent,
	}
	for _, t := range s.Temperatures {
		v[MetricTemperature+":"+t.Sensor] = t.Celsius
	}
	for _, d := range s.Disks {
		v[MetricDisk+":"+d.Path] = d.UsedPercent
	}
	for _, n := range s.Network {
		v[MetricNetworkReceive+":"+n.Name] = n.ReceiveBytesPerSecond
		v[MetricNetworkSend+":"+n.Name] = n.SendBytesPerSecond
	}
	return v
}
