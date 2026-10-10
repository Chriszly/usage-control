package router

import (
	"bytes"
	"context"
	"encoding/hex"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBEROIDRoundTrip(t *testing.T) {
	for _, oid := range []string{"1.3.6.1.2.1.1.3.0", "1.3.6.1.2.1.31.1.1.1.6.4294967295", "2.999.3", "0.0"} {
		encoded, err := berOID(oid)
		if err != nil {
			t.Fatalf("%s: %v", oid, err)
		}
		r := &berReader{data: encoded}
		content, _, err := r.expect(tagOID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := parseOID(content)
		if err != nil || got != oid {
			t.Errorf("%s came back as %q, %v", oid, got, err)
		}
	}
	for _, oid := range []string{"1", "3.1", "1.40", "1.3.x", "1.3.4294967296"} {
		if _, err := berOID(oid); err == nil {
			t.Errorf("%s was encoded", oid)
		}
	}
}

func TestBERIntegerRoundTrip(t *testing.T) {
	for _, value := range []int64{0, 1, 127, 128, 255, 256, -1, -128, -129, 1 << 31, -1 << 40, 1<<63 - 1} {
		r := &berReader{data: berInteger(value)}
		got, err := r.integer()
		if err != nil || got != value {
			t.Errorf("%d came back as %d, %v", value, got, err)
		}
	}
}

func TestBERLongLength(t *testing.T) {
	content := bytes.Repeat([]byte{'x'}, 300)
	r := &berReader{data: berOctets(content)}
	got, start, err := r.expect(tagOctetString)
	if err != nil || !bytes.Equal(got, content) || start != 4 {
		t.Fatalf("got %d bytes at %d, %v", len(got), start, err)
	}
	for _, broken := range [][]byte{{0x04}, {0x04, 0x05, 'a'}, {0x04, 0x80}, {0x04, 0x85, 1, 2, 3, 4, 5}} {
		if _, _, err := (&berReader{data: broken}).expect(tagOctetString); err == nil {
			t.Errorf("% x was read", broken)
		}
	}
}

func TestBERUnsigned(t *testing.T) {
	value, err := parseValue(tagCounter64, []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	if err != nil || value.number != 1<<64-1 {
		t.Fatalf("got %d, %v", value.number, err)
	}
	if _, err := parseValue(tagCounter64, []byte{0x01, 0, 0, 0, 0, 0, 0, 0, 0}); err == nil {
		t.Error("a counter of more than 64 bits was read")
	}
	if n, ok := (snmpValue{tag: tagInteger, signed: -3}).numeric(); ok {
		t.Errorf("a negative integer was read as %d", n)
	}
}

// The example of RFC 3414 A.3.2: the SHA key of "maplesyrup" for the
// engine 00 00 00 00 00 00 00 00 00 00 00 02.
func TestPasswordToKeyRFC3414(t *testing.T) {
	engine, _ := hex.DecodeString("000000000000000000000002")
	key := localizeKey(passwordToKey("maplesyrup"), engine)
	if got := hex.EncodeToString(key); got != "6695febc9288e36282235fc7151f128497b38f3f" {
		t.Errorf("got %s", got)
	}
}

func TestAESCFBRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 20)
	salt := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	plain := []byte("a scoped PDU of an odd length")
	encrypted, err := aesCFB(key, 3, 1234, salt, plain, true)
	if err != nil || bytes.Equal(encrypted, plain) {
		t.Fatalf("encrypted to % x, %v", encrypted, err)
	}
	decrypted, err := aesCFB(key, 3, 1234, salt, encrypted, false)
	if err != nil || !bytes.Equal(decrypted, plain) {
		t.Fatalf("decrypted to %q, %v", decrypted, err)
	}
	if _, err := aesCFB(key, 3, 1234, salt[:4], plain, true); err == nil {
		t.Error("a short salt was taken")
	}
}

// fakeSNMPAgent answers SNMP GET and GETBULK on a UDP port of 127.0.0.1
// from a fixed MIB, over v2c with a community or v3 with a user.
type fakeSNMPAgent struct {
	t    *testing.T
	conn *net.UDPConn

	mu        sync.Mutex
	community string
	// agent signs and encrypts the v3 answers with the user's keys.
	agent *snmpClient
	user  string
	mib   map[string]snmpValue
	oids  []string
	// requests counts the requests that were answered.
	requests int
	// stale makes the next v3 request get a notInTimeWindows report.
	stale bool
	// forge breaks the signature of the v3 answers.
	forge bool
}

func newFakeSNMPAgent(t *testing.T, mib map[string]snmpValue) *fakeSNMPAgent {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	f := &fakeSNMPAgent{t: t, conn: conn, community: "public"}
	f.setMIB(mib)
	go f.serve()
	return f
}

func (f *fakeSNMPAgent) address() string { return f.conn.LocalAddr().String() }

func (f *fakeSNMPAgent) withUser(user, password string) {
	engine, _ := hex.DecodeString("80001f8880e9630000d61ff449")
	auth := localizeKey(passwordToKey(password), engine)
	f.agent = &snmpClient{engineID: engine, boots: 7, engineTime: 100000, timeAt: time.Now(), authKey: auth, privKey: auth[:16], user: user, salt: 99}
	f.user = user
}

func (f *fakeSNMPAgent) setMIB(mib map[string]snmpValue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mib = mib
	f.oids = f.oids[:0]
	for oid := range mib {
		f.oids = append(f.oids, oid)
	}
	slices.SortFunc(f.oids, compareOIDs)
}

func (f *fakeSNMPAgent) serve() {
	buffer := make([]byte, snmpMaxMessage)
	for {
		n, from, err := f.conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		f.mu.Lock()
		answer := f.answer(bytes.Clone(buffer[:n]))
		f.mu.Unlock()
		if answer != nil {
			_, _ = f.conn.WriteToUDP(answer, from)
		}
	}
}

// answer returns the answer to a message, or nil to send none.
func (f *fakeSNMPAgent) answer(message []byte) []byte {
	r := &berReader{data: message}
	content, start, err := r.expect(tagSequence)
	if err != nil {
		f.t.Errorf("the agent got no SNMP message: %v", err)
		return nil
	}
	fields := inside(content, start)
	version, err := fields.integer()
	if err != nil {
		f.t.Errorf("the agent got no version: %v", err)
		return nil
	}
	if version == 1 {
		community, _, err := fields.expect(tagOctetString)
		if err != nil || f.agent != nil || string(community) != f.community {
			return nil // A wrong community gets no answer, as on a router.
		}
		tag, pdu, pduStart, _ := fields.next()
		id, binds := f.respond(inside(pdu, pduStart), tag)
		return berSequence(tagSequence, berInteger(1), berOctets(community), encodeAnswer(pduResponse, id, binds))
	}
	m, err := parseV3(message)
	if err != nil || f.agent == nil {
		f.t.Errorf("the agent got an SNMPv3 message it cannot read: %v", err)
		return nil
	}
	if len(m.engineID) == 0 {
		return f.unauthenticated(m.msgID, usmUnknownEngine)
	}
	if m.user != f.user {
		return f.unauthenticated(m.msgID, usmUnknownUser)
	}
	check := bytes.Clone(message)
	clear(check[m.authStart : m.authStart+12])
	if m.flags != 0x07 || !bytes.Equal(authCode(f.agent.authKey, check), m.authParams) {
		return f.unauthenticated(m.msgID, usmWrongDigest)
	}
	if f.stale {
		f.stale = false
		return f.unauthenticated(m.msgID, usmNotInTime)
	}
	scoped, err := aesCFB(f.agent.privKey, m.boots, m.engineTime, m.privParams, m.data, false)
	if err != nil {
		f.t.Error(err)
		return nil
	}
	sr := &berReader{data: scoped}
	sContent, sStart, err := sr.expect(tagSequence)
	if err != nil {
		f.t.Errorf("the agent could not decrypt the request: %v", err)
		return nil
	}
	s := inside(sContent, sStart)
	_, _, _ = s.expect(tagOctetString)
	_, _, _ = s.expect(tagOctetString)
	tag, pdu, pduStart, _ := s.next()
	id, binds := f.respond(inside(pdu, pduStart), tag)
	answer, err := f.agent.encodeV3(encodeAnswer(pduResponse, id, binds), int32(m.msgID)) //nolint:gosec // a test's request id
	if err != nil {
		f.t.Error(err)
		return nil
	}
	if f.forge {
		parsed, _ := parseV3(answer)
		answer[parsed.authStart] ^= 0xff
	}
	return answer
}

// unauthenticated returns a report of the USM, as a router sends it to a
// request it does not take.
func (f *fakeSNMPAgent) unauthenticated(msgID int64, oid string) []byte {
	security := berSequence(tagSequence, berOctets(f.agent.engineID), berInteger(f.agent.boots), berInteger(f.agent.engineTime),
		berOctets(nil), berOctets(nil), berOctets(nil))
	report := encodeAnswer(pduReport, 0, []varbind{{oid: oid, value: snmpValue{tag: tagCounter32, number: 1}}})
	return berSequence(tagSequence,
		berInteger(3),
		berSequence(tagSequence, berInteger(msgID), berInteger(snmpMaxMessage), berOctets([]byte{0}), berInteger(3)),
		berOctets(security),
		berSequence(tagSequence, berOctets(f.agent.engineID), berOctets(nil), report),
	)
}

// respond answers a GET or GETBULK PDU; the reader sends no other.
func (f *fakeSNMPAgent) respond(pdu *berReader, tag byte) (int32, []varbind) {
	if tag != pduGet && tag != pduGetBulk {
		f.t.Errorf("the agent got the PDU 0x%02x, which does not only read", tag)
	}
	id, _ := pdu.integer()
	nonRepeaters, _ := pdu.integer()
	maxRepetitions, _ := pdu.integer()
	list, listStart, _ := pdu.expect(tagSequence)
	binds := inside(list, listStart)
	var asked []string
	for len(binds.data) > 0 {
		bind, bindStart, _ := binds.expect(tagSequence)
		oid, _, _ := inside(bind, bindStart).expect(tagOID)
		parsed, _ := parseOID(oid)
		asked = append(asked, parsed)
	}
	f.requests++
	var out []varbind
	if nonRepeaters == 0 && maxRepetitions == 0 {
		for _, oid := range asked {
			value, ok := f.mib[oid]
			if !ok {
				value = snmpValue{tag: tagNoSuchInst, missing: true}
			}
			out = append(out, varbind{oid: oid, value: value})
		}
		return int32(id), out //nolint:gosec // a test's request id
	}
	for _, oid := range asked {
		next := oid
		for range maxRepetitions {
			i, _ := slices.BinarySearchFunc(f.oids, next, compareOIDs)
			for i < len(f.oids) && f.oids[i] == next {
				i++
			}
			if i == len(f.oids) {
				out = append(out, varbind{oid: next, value: snmpValue{tag: tagEndOfMIB, missing: true}})
				break
			}
			next = f.oids[i]
			out = append(out, varbind{oid: next, value: f.mib[next]})
		}
	}
	return int32(id), out //nolint:gosec // a test's request id
}

func encodeAnswer(tag byte, id int32, binds []varbind) []byte {
	var encoded [][]byte
	for _, bind := range binds {
		oid, _ := berOID(bind.oid)
		encoded = append(encoded, berSequence(tagSequence, oid, encodeValue(bind.value)))
	}
	return berSequence(tag, berInteger(int64(id)), berInteger(0), berInteger(0), berSequence(tagSequence, encoded...))
}

func encodeValue(v snmpValue) []byte {
	switch v.tag {
	case tagInteger:
		return berInteger(v.signed)
	case tagCounter32, tagGauge32, tagTimeTicks, tagCounter64:
		content := []byte{byte(v.number & 0xff)}
		for n := v.number >> 8; n > 0; n >>= 8 {
			content = append([]byte{byte(n & 0xff)}, content...)
		}
		if content[0]&0x80 != 0 {
			content = append([]byte{0}, content...)
		}
		return berElement(v.tag, content)
	case tagOID:
		encoded, _ := berOID(v.text)
		return encoded
	default:
		if v.missing {
			return berElement(v.tag, nil)
		}
		return berElement(v.tag, []byte(v.text))
	}
}

func integer(n int64) snmpValue    { return snmpValue{tag: tagInteger, signed: n} }
func counter32(n uint64) snmpValue { return snmpValue{tag: tagCounter32, number: n} }
func counter64(n uint64) snmpValue { return snmpValue{tag: tagCounter64, number: n} }
func gauge(n uint64) snmpValue     { return snmpValue{tag: tagGauge32, number: n} }
func text(s string) snmpValue      { return snmpValue{tag: tagOctetString, text: s} }
func objectID(s string) snmpValue  { return snmpValue{tag: tagOID, text: s} }
func timeTicks(n uint64) snmpValue { return snmpValue{tag: tagTimeTicks, number: n} }

// routerMIB is a router with a WAN and a LAN port, a loopback, a port that
// is down and one that only has 32-bit counters, two cores and 256 MiB of
// RAM.
func routerMIB(received, sent, uptimeTicks uint64) map[string]snmpValue {
	mib := map[string]snmpValue{
		oidUptime:                    timeTicks(uptimeTicks),
		"1.3.6.1.2.1.1.5.0":          text("router"),
		"1.3.6.1.2.1.25.2.3.1.2.1":   objectID(oidStorageTypeRAM),
		"1.3.6.1.2.1.25.2.3.1.4.1":   integer(1024),
		"1.3.6.1.2.1.25.2.3.1.5.1":   integer(262144),
		"1.3.6.1.2.1.25.2.3.1.6.1":   integer(65536),
		"1.3.6.1.2.1.25.2.3.1.2.31":  objectID("1.3.6.1.2.1.25.2.1.4"),
		"1.3.6.1.2.1.25.3.3.1.2.196": integer(10),
		"1.3.6.1.2.1.25.3.3.1.2.197": integer(30),
	}
	type port struct {
		index, kind, status int64
		name                string
		hc                  bool
	}
	for _, p := range []port{
		{1, 24, 1, "lo", true},
		{2, 6, 1, "eth0", true},
		{3, 6, 1, "eth1", true},
		{4, 6, 2, "eth2", true},
		{5, 6, 1, "", false},
	} {
		row := "." + strconv.FormatInt(p.index, 10)
		mib[oidIfType+row] = integer(p.kind)
		mib[oidIfOperStatus+row] = integer(p.status)
		mib[oidIfDescr+row] = text("port " + strconv.FormatInt(p.index, 10))
		mib[oidIfInOctets+row] = counter32(received & 0xffffffff)
		mib[oidIfOutOctets+row] = counter32(sent & 0xffffffff)
		mib[oidIfInErrors+row] = counter32(2)
		mib[oidIfOutErrors+row] = counter32(1)
		mib[oidIfInDiscards+row] = counter32(4)
		mib[oidIfOutDiscards+row] = counter32(0)
		if p.name != "" {
			mib[oidIfName+row] = text(p.name)
		}
		if p.hc {
			mib[oidIfHCInOctets+row] = counter64(received)
			mib[oidIfHCOutOctets+row] = counter64(sent)
			mib[oidIfHighSpeed+row] = gauge(1000)
		}
	}
	return mib
}

func newTestSNMP(t *testing.T, agent *fakeSNMPAgent, v3 bool, user, password string) *SNMPReader {
	t.Helper()
	var reader *SNMPReader
	if v3 {
		reader = NewSNMPv3(agent.address(), user, password)
	} else {
		reader = NewSNMP(agent.address(), password)
	}
	return reader
}

func TestSNMPReadsTheRouter(t *testing.T) {
	for _, v3 := range []bool{false, true} {
		t.Run(map[bool]string{false: "v2c", true: "v3"}[v3], func(t *testing.T) {
			agent := newFakeSNMPAgent(t, routerMIB(5<<32, 1000, 360000))
			password := "public"
			if v3 {
				agent.withUser("monitor", "a long password")
				password = "a long password"
			}
			reader := newTestSNMP(t, agent, v3, "monitor", password)
			snapshot, err := reader.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.UptimeSeconds != 3600 {
				t.Errorf("uptime %d, want 3600", snapshot.UptimeSeconds)
			}
			var names []string
			for _, n := range snapshot.Network {
				names = append(names, n.Name)
			}
			if !slices.Equal(names, []string{"eth0", "eth1", "port 5"}) {
				t.Fatalf("interfaces %v, want eth0, eth1 and port 5", names)
			}
			eth0 := snapshot.Network[0]
			if eth0.ReceivedBytes != 5<<32 || eth0.SentBytes != 1000 || eth0.LinkMbps != 1000 || eth0.Errors != 3 || eth0.Dropped != 4 {
				t.Errorf("eth0 is %+v", eth0)
			}
			if snapshot.Network[2].ReceivedBytes != 0 || snapshot.Network[2].LinkMbps != 0 {
				t.Errorf("the port with 32-bit counters is %+v", snapshot.Network[2])
			}
			if snapshot.CPU.Cores != 2 || snapshot.CPU.UsagePercent != 20 || !slices.Equal(snapshot.CPU.CoreUsagePercent, []float64{10, 30}) {
				t.Errorf("CPU is %+v", snapshot.CPU)
			}
			if snapshot.Memory.TotalBytes != 256<<20 || snapshot.Memory.UsedBytes != 64<<20 || snapshot.Memory.UsedPercent != 25 {
				t.Errorf("memory is %+v", snapshot.Memory)
			}
		})
	}
}

func TestSNMPTrafficRates(t *testing.T) {
	reader := &SNMPReader{}
	start := time.Now()
	columns := func(received uint64) map[string]map[string]snmpValue {
		return map[string]map[string]snmpValue{
			oidIfOperStatus:  {"2": integer(1)},
			oidIfName:        {"2": text("eth0")},
			oidIfHCInOctets:  {"2": counter64(received)},
			oidIfHCOutOctets: {"2": counter64(received / 2)},
		}
	}
	reader.network(columns(1000), start, false)
	got := reader.network(columns(11000), start.Add(10*time.Second), false)
	if len(got) != 1 || got[0].ReceiveBytesPerSecond != 1000 || got[0].SendBytesPerSecond != 500 {
		t.Fatalf("got %+v", got)
	}
	got = reader.network(columns(500), start.Add(20*time.Second), true)
	if got[0].ReceiveBytesPerSecond != 0 {
		t.Errorf("a restart counted %v bytes per second", got[0].ReceiveBytesPerSecond)
	}
}

func TestSNMPWithoutHostResources(t *testing.T) {
	mib := routerMIB(100, 100, 100)
	for oid := range mib {
		if strings.HasPrefix(oid, "1.3.6.1.2.1.25.") {
			delete(mib, oid)
		}
	}
	agent := newFakeSNMPAgent(t, mib)
	snapshot, err := newTestSNMP(t, agent, false, "", "public").Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CPU.Cores != 0 || snapshot.Memory.TotalBytes != 0 || len(snapshot.Network) != 3 {
		t.Errorf("got %+v", snapshot)
	}
}

func TestSNMPv2cWrongCommunity(t *testing.T) {
	agent := newFakeSNMPAgent(t, routerMIB(1, 1, 1))
	reader := newTestSNMP(t, agent, false, "", "private")
	_, err := reader.Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not answer SNMP") {
		t.Fatalf("got %v", err)
	}
}

func TestSNMPv3Refusals(t *testing.T) {
	agent := newFakeSNMPAgent(t, routerMIB(1, 1, 1))
	agent.withUser("monitor", "the right password")

	_, err := newTestSNMP(t, agent, true, "monitor", "a wrong password").Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refused the SNMPv3 password") {
		t.Errorf("a wrong password: %v", err)
	}
	_, err = newTestSNMP(t, agent, true, "someone", "the right password").Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not know the SNMPv3 user") {
		t.Errorf("an unknown user: %v", err)
	}

	// A clock that moved on is taken, and the request sent again.
	reader := newTestSNMP(t, agent, true, "monitor", "the right password")
	if _, err := reader.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	agent.mu.Lock()
	agent.stale = true
	agent.mu.Unlock()
	if _, err := reader.Collect(context.Background()); err != nil {
		t.Errorf("after a notInTimeWindows report: %v", err)
	}
}

func TestSNMPv3AnswerMustBeSigned(t *testing.T) {
	agent := newFakeSNMPAgent(t, routerMIB(1, 1, 1))
	agent.withUser("monitor", "the right password")
	reader := newTestSNMP(t, agent, true, "monitor", "the right password")
	if _, err := reader.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	// An answer with a wrong signature is refused.
	agent.mu.Lock()
	agent.forge = true
	agent.mu.Unlock()
	_, err := reader.Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Errorf("got %v", err)
	}
}

func TestSNMPPrefersUCDMemoryAndSystemUptime(t *testing.T) {
	mib := routerMIB(1, 1, 360000)
	mib[oidSystemUptime] = timeTicks(8640000)
	mib[oidMemTotal] = integer(262144)
	mib[oidMemFree] = integer(16384)
	mib[oidMemBuffer] = integer(16384)
	mib[oidMemCached] = integer(32768)
	agent := newFakeSNMPAgent(t, mib)
	snapshot, err := newTestSNMP(t, agent, false, "", "public").Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.UptimeSeconds != 86400 {
		t.Errorf("uptime %d, want the system's 86400", snapshot.UptimeSeconds)
	}
	// Buffers and cache count as available.
	if snapshot.Memory.TotalBytes != 256<<20 || snapshot.Memory.AvailableBytes != 64<<20 || snapshot.Memory.UsedPercent != 75 {
		t.Errorf("memory is %+v", snapshot.Memory)
	}
	agent.mu.Lock()
	mib[oidMemAvailable] = integer(131072)
	agent.mu.Unlock()
	snapshot, err = newTestSNMP(t, agent, false, "", "public").Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Memory.AvailableBytes != 128<<20 {
		t.Errorf("memory is %+v, want memAvailable", snapshot.Memory)
	}
}

func TestSNMPWalkOutOfOrder(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	// An agent that answers every walk with the same two rows, the second
	// before the first.
	go func() {
		buffer := make([]byte, snmpMaxMessage)
		for {
			n, from, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			outer, start, _ := (&berReader{data: buffer[:n]}).expect(tagSequence)
			fields := inside(outer, start)
			_, _ = fields.integer()
			community, _, _ := fields.expect(tagOctetString)
			_, pdu, pduStart, _ := fields.next()
			id, _ := inside(pdu, pduStart).integer()
			answer := encodeAnswer(pduResponse, int32(id), []varbind{ //nolint:gosec // a test's request id
				{oid: oidIfType + ".2", value: integer(6)},
				{oid: oidIfType + ".1", value: integer(6)},
			})
			_, _ = conn.WriteToUDP(berSequence(tagSequence, berInteger(1), berOctets(community), answer), from)
		}
	}()
	client := newSNMPv2c(conn.LocalAddr().String(), "public")
	if _, err := client.walk(context.Background(), oidIfType); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Errorf("got %v", err)
	}
}
