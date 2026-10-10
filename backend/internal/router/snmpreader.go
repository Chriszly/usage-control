package router

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// The standard OIDs an SNMP reader asks, which every SNMP router has with
// the same ids: the uptime, the interfaces (IF-MIB) and CPU and memory
// (HOST-RESOURCES-MIB).
const (
	oidUptime         = "1.3.6.1.2.1.1.3.0"
	oidIfType         = "1.3.6.1.2.1.2.2.1.3"
	oidIfOperStatus   = "1.3.6.1.2.1.2.2.1.8"
	oidIfInOctets     = "1.3.6.1.2.1.2.2.1.10"
	oidIfInDiscards   = "1.3.6.1.2.1.2.2.1.13"
	oidIfInErrors     = "1.3.6.1.2.1.2.2.1.14"
	oidIfOutOctets    = "1.3.6.1.2.1.2.2.1.16"
	oidIfOutDiscards  = "1.3.6.1.2.1.2.2.1.19"
	oidIfOutErrors    = "1.3.6.1.2.1.2.2.1.20"
	oidIfDescr        = "1.3.6.1.2.1.2.2.1.2"
	oidIfName         = "1.3.6.1.2.1.31.1.1.1.1"
	oidIfHCInOctets   = "1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOutOctets  = "1.3.6.1.2.1.31.1.1.1.10"
	oidIfHighSpeed    = "1.3.6.1.2.1.31.1.1.1.15"
	oidProcessorLoad  = "1.3.6.1.2.1.25.3.3.1.2"
	oidStorageType    = "1.3.6.1.2.1.25.2.3.1.2"
	oidStorageUnits   = "1.3.6.1.2.1.25.2.3.1.4"
	oidStorageSize    = "1.3.6.1.2.1.25.2.3.1.5"
	oidStorageUsed    = "1.3.6.1.2.1.25.2.3.1.6"
	oidStorageTypeRAM = "1.3.6.1.2.1.25.2.1.2"
	oidSystemUptime   = "1.3.6.1.2.1.25.1.1.0"

	// The memory in UCD-SNMP-MIB, in KiB, of net-snmp, which Linux-based
	// routers run: hrStorage counts buffers and cache there as used.
	oidMemTotal     = "1.3.6.1.4.1.2021.4.5.0"
	oidMemFree      = "1.3.6.1.4.1.2021.4.6.0"
	oidMemBuffer    = "1.3.6.1.4.1.2021.4.14.0"
	oidMemCached    = "1.3.6.1.4.1.2021.4.15.0"
	oidMemAvailable = "1.3.6.1.4.1.2021.4.27.0" // net-snmp 5.9 and later
)

// The interface types that are left out: loopback, and those that only
// carry the traffic of others again.
var skippedIfTypes = map[uint64]bool{
	24:  true, // softwareLoopback
	131: true, // tunnel
	53:  true, // propVirtual
}

// SNMPReader reads a router over SNMP: the traffic of each interface, the
// uptime, and CPU and memory where the router tells them.
type SNMPReader struct {
	client *snmpClient

	mu      sync.Mutex
	traffic map[string]*[2]counter
	uptime  uint64
	read    bool
}

// NewSNMP returns a reader for the router at address with SNMPv2c and a
// community.
func NewSNMP(address, community string) *SNMPReader {
	return &SNMPReader{client: newSNMPv2c(address, community)}
}

// NewSNMPv3 returns a reader for the router at address with SNMPv3 and a
// user, whose password signs and encrypts every request.
func NewSNMPv3(address, user, password string) *SNMPReader {
	return &SNMPReader{client: newSNMPv3(address, user, password)}
}

// Collect reads the router's usage.
func (s *SNMPReader) Collect(ctx context.Context) (metrics.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The system's uptime where the router tells it; sysUpTime is the SNMP
	// agent's, which starts again when the agent restarts.
	uptime, err := s.client.get(ctx, oidSystemUptime, oidUptime)
	if err != nil {
		return metrics.Snapshot{}, err
	}
	var snapshot metrics.Snapshot
	for _, bind := range uptime {
		if ticks, ok := bind.value.numeric(); ok {
			snapshot.UptimeSeconds = ticks / 100
			break
		}
	}
	restarted := s.read && snapshot.UptimeSeconds < s.uptime
	s.uptime, s.read = snapshot.UptimeSeconds, true

	columns := map[string]map[string]snmpValue{}
	for _, column := range []string{
		oidIfType, oidIfOperStatus, oidIfDescr, oidIfName, oidIfHCInOctets, oidIfHCOutOctets, oidIfInOctets, oidIfOutOctets,
		oidIfHighSpeed, oidIfInErrors, oidIfOutErrors, oidIfInDiscards, oidIfOutDiscards,
	} {
		rows, err := s.column(ctx, column)
		if err != nil {
			return metrics.Snapshot{}, err
		}
		columns[column] = rows
	}
	snapshot.Network = s.network(columns, time.Now(), restarted)

	// CPU and memory are in HOST-RESOURCES-MIB, which not every router has.
	if loads, err := s.column(ctx, oidProcessorLoad); err == nil {
		snapshot.CPU = processorLoad(loads)
	}
	if memory, err := s.memory(ctx); err == nil {
		snapshot.Memory = memory
	}
	return snapshot, nil
}

// column walks one column of a table and returns its values by row index.
func (s *SNMPReader) column(ctx context.Context, oid string) (map[string]snmpValue, error) {
	binds, err := s.client.walk(ctx, oid)
	if err != nil {
		return nil, err
	}
	rows := map[string]snmpValue{}
	for _, bind := range binds {
		if !bind.value.missing {
			rows[strings.TrimPrefix(bind.oid, oid+".")] = bind.value
		}
	}
	return rows, nil
}

// network turns the interface table into the traffic of each interface
// that is up, by its name, measured since the reading before.
func (s *SNMPReader) network(columns map[string]map[string]snmpValue, now time.Time, restarted bool) []metrics.NetworkInterface {
	if s.traffic == nil {
		s.traffic = map[string]*[2]counter{}
	}
	number := func(column, row string) (uint64, bool) {
		return columns[column][row].numeric()
	}
	var rows []string
	for row := range columns[oidIfOperStatus] {
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b string) int {
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		return x - y
	})
	var interfaces []metrics.NetworkInterface
	seen := map[string]bool{}
	for _, row := range rows {
		if status, _ := number(oidIfOperStatus, row); status != 1 {
			continue
		}
		if kind, _ := number(oidIfType, row); skippedIfTypes[kind] {
			continue
		}
		name := columns[oidIfName][row].text
		if name == "" {
			name = columns[oidIfDescr][row].text
		}
		name = strings.TrimSpace(strings.ToValidUTF8(name, ""))
		if name == "" || seen[name] {
			name = "if" + row
		}
		seen[name] = true
		// The 64-bit counters, where the router has them; the 32-bit ones
		// wrap within seconds on a fast link, which the counters handle at
		// every reading.
		received, ok := number(oidIfHCInOctets, row)
		sent, okSent := number(oidIfHCOutOctets, row)
		wide := ok && okSent
		if !wide {
			received, ok = number(oidIfInOctets, row)
			sent, okSent = number(oidIfOutOctets, row)
			if !ok || !okSent {
				continue
			}
		}
		counters := s.traffic[name]
		if counters == nil {
			counters = &[2]counter{}
			s.traffic[name] = counters
		}
		network := metrics.NetworkInterface{Name: name, ReceivedBytes: received, SentBytes: sent}
		rate := (*counter).rate
		if wide {
			rate = (*counter).rate64
		}
		network.ReceiveBytesPerSecond, _ = rate(&counters[0], received, now, restarted)
		network.SendBytesPerSecond, _ = rate(&counters[1], sent, now, restarted)
		if speed, ok := number(oidIfHighSpeed, row); ok && speed > 0 && speed < 1<<31 {
			network.LinkMbps = int(speed)
		}
		inErrors, _ := number(oidIfInErrors, row)
		outErrors, _ := number(oidIfOutErrors, row)
		inDiscards, _ := number(oidIfInDiscards, row)
		outDiscards, _ := number(oidIfOutDiscards, row)
		network.Errors, network.Dropped = inErrors+outErrors, inDiscards+outDiscards
		interfaces = append(interfaces, network)
	}
	return interfaces
}

// processorLoad turns hrProcessorLoad, the usage of each core over the
// last minute in percent, into the CPU usage.
func processorLoad(loads map[string]snmpValue) metrics.CPU {
	var rows []string
	for row := range loads {
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b string) int {
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		return x - y
	})
	var cores []float64
	var sum float64
	for _, row := range rows {
		load, ok := loads[row].numeric()
		if !ok || load > 100 {
			continue
		}
		cores = append(cores, float64(load))
		sum += float64(load)
	}
	if len(cores) == 0 {
		return metrics.CPU{}
	}
	return metrics.CPU{UsagePercent: sum / float64(len(cores)), Cores: len(cores), CoreUsagePercent: cores}
}

// memory reads the RAM from UCD-SNMP-MIB, or else from hrStorage.
func (s *SNMPReader) memory(ctx context.Context) (metrics.Memory, error) {
	if binds, err := s.client.get(ctx, oidMemTotal, oidMemFree, oidMemBuffer, oidMemCached, oidMemAvailable); err == nil && len(binds) == 5 {
		var kib [5]uint64
		var known [5]bool
		for i, bind := range binds {
			kib[i], known[i] = bind.value.numeric()
		}
		total, available := kib[0], kib[4]
		if !known[4] && known[1] {
			available = kib[1] + kib[2] + kib[3]
		}
		if known[0] && total > 0 && total < 1<<40 && (known[4] || known[1]) && available <= total {
			return metrics.Memory{
				TotalBytes: total << 10, UsedBytes: (total - available) << 10, AvailableBytes: available << 10,
				UsedPercent: float64(total-available) / float64(total) * 100,
			}, nil
		}
	}
	types, err := s.column(ctx, oidStorageType)
	if err != nil {
		return metrics.Memory{}, err
	}
	for row, kind := range types {
		if kind.text != oidStorageTypeRAM {
			continue
		}
		binds, err := s.client.get(ctx, oidStorageUnits+"."+row, oidStorageSize+"."+row, oidStorageUsed+"."+row)
		if err != nil {
			return metrics.Memory{}, fmt.Errorf("read the memory of %s: %w", s.client.address, err)
		}
		if len(binds) != 3 {
			break
		}
		units, okUnits := binds[0].value.numeric()
		size, okSize := binds[1].value.numeric()
		used, okUsed := binds[2].value.numeric()
		if !okUnits || !okSize || !okUsed || size == 0 || used > size || units == 0 || units > 1<<20 {
			break
		}
		total, usedBytes := size*units, used*units
		return metrics.Memory{
			TotalBytes: total, UsedBytes: usedBytes, AvailableBytes: total - usedBytes,
			UsedPercent: float64(used) / float64(size) * 100,
		}, nil
	}
	return metrics.Memory{}, fmt.Errorf("the router %s tells no memory", s.client.address)
}
