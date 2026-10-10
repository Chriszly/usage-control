package router

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // SNMPv3's HMAC-SHA-96 (RFC 3414), which routers speak
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// snmpPort is where routers answer SNMP.
	snmpPort = "161"
	// snmpTimeout is how long one SNMP request waits for its answer; it is
	// sent twice before the router counts as not answering.
	snmpTimeout = 1500 * time.Millisecond
	// snmpMaxMessage is the largest SNMP answer that is read.
	snmpMaxMessage = 65507
)

// The USM reports a router answers a request it does not take with
// (RFC 3414).
const (
	usmUnsupportedSecLevel = "1.3.6.1.6.3.15.1.1.1.0"
	usmDecryptionError     = "1.3.6.1.6.3.15.1.1.6.0"
	usmUnknownUser         = "1.3.6.1.6.3.15.1.1.3.0"
	usmNotInTime           = "1.3.6.1.6.3.15.1.1.2.0"
	usmUnknownEngine       = "1.3.6.1.6.3.15.1.1.4.0"
	usmWrongDigest         = "1.3.6.1.6.3.15.1.1.5.0"
)

// varbind is one variable of an SNMP answer.
type varbind struct {
	oid   string
	value snmpValue
}

// snmpClient asks a router over SNMP, v2c with a community or v3 with a
// user whose password authenticates (HMAC-SHA-96) and encrypts (AES-128)
// every request. It only sends GET and GETBULK, which read; never SET.
type snmpClient struct {
	address   string
	community string
	v3        bool
	user      string
	// masterKey is the user's password turned into a key (RFC 3414 A.2),
	// localized to the router's engine as authKey and privKey once known.
	masterKey        []byte
	authKey, privKey []byte
	control          func(network, address string, c syscall.RawConn) error

	mu         sync.Mutex
	requestID  int32
	salt       uint64
	engineID   []byte
	boots      int64
	engineTime int64
	timeAt     time.Time
}

// newSNMPv2c returns a client of the router at address (host or
// host:port) with a community.
func newSNMPv2c(address, community string) *snmpClient {
	return &snmpClient{address: withPort(address, snmpPort), community: community, control: localNetworkOnly, requestID: randomID()}
}

// newSNMPv3 returns a client of the router at address with a user and its
// password, used for both authentication and encryption.
func newSNMPv3(address, user, password string) *snmpClient {
	var salt [8]byte
	_, _ = rand.Read(salt[:])
	return &snmpClient{
		address: withPort(address, snmpPort), v3: true, user: user, masterKey: passwordToKey(password),
		control: localNetworkOnly, requestID: randomID(), salt: binary.BigEndian.Uint64(salt[:]),
	}
}

func withPort(address, port string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(strings.Trim(address, "[]"), port)
}

func randomID() int32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return int32(binary.BigEndian.Uint32(b[:]) & 0x3fffffff)
}

// get reads the variables named by oids.
func (c *snmpClient) get(ctx context.Context, oids ...string) ([]varbind, error) {
	return c.request(ctx, pduGet, oids, 0, 0)
}

// walk reads every variable below root, such as a column of a table.
func (c *snmpClient) walk(ctx context.Context, root string) ([]varbind, error) {
	var all []varbind
	next := root
	// A table of 4096 rows is far more than a router has.
	for len(all) < 4096 {
		answer, err := c.request(ctx, pduGetBulk, []string{next}, 0, 25)
		if err != nil {
			return nil, err
		}
		if len(answer) == 0 {
			return all, nil
		}
		for _, v := range answer {
			if v.value.tag == tagEndOfMIB || !strings.HasPrefix(v.oid, root+".") {
				return all, nil
			}
			if compareOIDs(v.oid, next) <= 0 {
				// An agent that does not move on would be asked forever.
				return nil, fmt.Errorf("the router %s answered an SNMP walk out of order", c.address)
			}
			all = append(all, v)
			next = v.oid
		}
	}
	return all, nil
}

// request sends one PDU, a second time if no answer comes, and returns the
// variables of the answer.
func (c *snmpClient) request(ctx context.Context, pduType byte, oids []string, nonRepeaters, maxRepetitions int) ([]varbind, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.v3 && c.engineID == nil {
		if err := c.discover(ctx); err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		c.requestID = (c.requestID + 1) & 0x3fffffff
		id := c.requestID
		pdu, err := encodePDU(pduType, id, oids, nonRepeaters, maxRepetitions)
		if err != nil {
			return nil, err
		}
		var message []byte
		if c.v3 {
			message, err = c.encodeV3(pdu, id)
			if err != nil {
				return nil, err
			}
		} else {
			message = berSequence(tagSequence, berInteger(1), berOctets([]byte(c.community)), pdu)
		}
		answer, err := c.exchange(ctx, message)
		if err != nil {
			return nil, err
		}
		var tag byte
		var binds []varbind
		if c.v3 {
			tag, binds, err = c.decodeV3(answer, id)
		} else {
			tag, binds, err = decodeV2c(answer, id)
		}
		if err != nil {
			return nil, err
		}
		if tag == pduReport {
			if retry, err := c.report(binds); !retry || attempt > 0 {
				if err == nil {
					err = errors.New("the router reported a problem with the request")
				}
				return nil, err
			}
			continue
		}
		return binds, nil
	}
}

// report handles a report of the USM: an engine clock that moved on is
// taken and the request sent again; anything else is an error.
func (c *snmpClient) report(binds []varbind) (retry bool, err error) {
	for _, bind := range binds {
		switch bind.oid {
		case usmNotInTime:
			return true, nil
		case usmUnknownEngine:
			c.engineID = nil
			return false, errors.New("the router's SNMP engine changed; it is asked again next time")
		case usmUnknownUser:
			return false, errors.New("the router does not know the SNMPv3 user")
		case usmWrongDigest:
			return false, errors.New("the router refused the SNMPv3 password; check it, and that the user authenticates with SHA (SHA-1)")
		case usmDecryptionError:
			return false, errors.New("the router could not decrypt the request; the SNMPv3 user must encrypt with AES (AES-128)")
		case usmUnsupportedSecLevel:
			return false, errors.New("the SNMPv3 user must both authenticate (SHA) and encrypt (AES-128)")
		}
	}
	return false, nil
}

// exchange sends a message and waits for an answer, sending it once more
// when none comes in time.
func (c *snmpClient) exchange(ctx context.Context, message []byte) ([]byte, error) {
	dialer := &net.Dialer{Timeout: snmpTimeout, Control: c.control}
	conn, err := dialer.DialContext(ctx, "udp", c.address)
	if err != nil {
		return nil, fmt.Errorf("ask %s over SNMP: %w", c.address, err)
	}
	defer func() { _ = conn.Close() }()
	buffer := make([]byte, snmpMaxMessage)
	for attempt := 0; attempt < 2; attempt++ {
		deadline := time.Now().Add(snmpTimeout)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetDeadline(deadline)
		if _, err := conn.Write(message); err != nil {
			return nil, fmt.Errorf("ask %s over SNMP: %w", c.address, err)
		}
		n, err := conn.Read(buffer)
		if err == nil {
			return buffer[:n], nil
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			// The router answered that nothing listens on the SNMP port.
			break
		}
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() || ctx.Err() != nil {
			return nil, fmt.Errorf("ask %s over SNMP: %w", c.address, err)
		}
	}
	return nil, fmt.Errorf("the router %s does not answer SNMP; switch it on in its settings and check the community or user", c.address)
}

// encodePDU encodes a request PDU; for GETBULK, the error fields are its
// non-repeaters and max-repetitions.
func encodePDU(pduType byte, id int32, oids []string, nonRepeaters, maxRepetitions int) ([]byte, error) {
	var binds [][]byte
	for _, oid := range oids {
		encoded, err := berOID(oid)
		if err != nil {
			return nil, err
		}
		binds = append(binds, berSequence(tagSequence, encoded, berElement(tagNull, nil)))
	}
	return berSequence(pduType, berInteger(int64(id)), berInteger(int64(nonRepeaters)), berInteger(int64(maxRepetitions)),
		berSequence(tagSequence, binds...)), nil
}

// decodePDU reads a response or report PDU and checks its request id.
func decodePDU(r *berReader, id int32) (byte, []varbind, error) {
	tag, content, start, err := r.next()
	if err != nil {
		return 0, nil, err
	}
	if tag != pduResponse && tag != pduReport {
		return 0, nil, fmt.Errorf("%w: unexpected PDU 0x%02x", errBER, tag)
	}
	pdu := inside(content, start)
	gotID, err := pdu.integer()
	if err != nil {
		return 0, nil, err
	}
	if tag == pduResponse && gotID != int64(id) {
		return 0, nil, errors.New("the router answered another request")
	}
	status, err := pdu.integer()
	if err != nil {
		return 0, nil, err
	}
	if _, err := pdu.integer(); err != nil {
		return 0, nil, err
	}
	if status != 0 {
		return 0, nil, fmt.Errorf("the router answered SNMP error %d", status)
	}
	list, listStart, err := pdu.expect(tagSequence)
	if err != nil {
		return 0, nil, err
	}
	binds := inside(list, listStart)
	var out []varbind
	for len(binds.data) > 0 {
		bind, bindStart, err := binds.expect(tagSequence)
		if err != nil {
			return 0, nil, err
		}
		fields := inside(bind, bindStart)
		oidContent, _, err := fields.expect(tagOID)
		if err != nil {
			return 0, nil, err
		}
		oid, err := parseOID(oidContent)
		if err != nil {
			return 0, nil, err
		}
		valueTag, valueContent, _, err := fields.next()
		if err != nil {
			return 0, nil, err
		}
		value, err := parseValue(valueTag, valueContent)
		if err != nil {
			return 0, nil, err
		}
		out = append(out, varbind{oid: oid, value: value})
	}
	return tag, out, nil
}

func decodeV2c(message []byte, id int32) (byte, []varbind, error) {
	outer := &berReader{data: message}
	content, start, err := outer.expect(tagSequence)
	if err != nil {
		return 0, nil, err
	}
	r := inside(content, start)
	if version, err := r.integer(); err != nil || version != 1 {
		return 0, nil, fmt.Errorf("%w: not an SNMPv2c answer", errBER)
	}
	if _, _, err := r.expect(tagOctetString); err != nil {
		return 0, nil, err
	}
	return decodePDU(r, id)
}

// passwordToKey turns a password into a key as RFC 3414 A.2.2 does for
// SHA: the hash of the password repeated to one megabyte.
func passwordToKey(password string) []byte {
	if password == "" {
		return nil
	}
	hash := sha1.New() //nolint:gosec // RFC 3414's key
	block := make([]byte, 64)
	for i, done := 0, 0; done < 1<<20; done += 64 {
		for j := range block {
			block[j] = password[i%len(password)]
			i++
		}
		hash.Write(block)
	}
	return hash.Sum(nil)
}

// localizeKey ties a key to the router's engine (RFC 3414 A.2.2).
func localizeKey(key, engineID []byte) []byte {
	hash := sha1.New() //nolint:gosec // RFC 3414's key
	hash.Write(key)
	hash.Write(engineID)
	hash.Write(key)
	return hash.Sum(nil)
}

// discover asks the router for its engine id, boots and clock, which every
// SNMPv3 request must carry, with a request that needs no user.
func (c *snmpClient) discover(ctx context.Context) error {
	c.requestID = (c.requestID + 1) & 0x3fffffff
	id := c.requestID
	pdu, err := encodePDU(pduGet, id, nil, 0, 0)
	if err != nil {
		return err
	}
	security := berSequence(tagSequence, berOctets(nil), berInteger(0), berInteger(0), berOctets(nil), berOctets(nil), berOctets(nil))
	message := berSequence(tagSequence,
		berInteger(3),
		berSequence(tagSequence, berInteger(int64(id)), berInteger(snmpMaxMessage), berOctets([]byte{0x04}), berInteger(3)),
		berOctets(security),
		berSequence(tagSequence, berOctets(nil), berOctets(nil), pdu),
	)
	answer, err := c.exchange(ctx, message)
	if err != nil {
		return err
	}
	parsed, err := parseV3(answer)
	if err != nil {
		return err
	}
	if len(parsed.engineID) == 0 || len(parsed.engineID) > 32 {
		return errors.New("the router answered without an SNMP engine id")
	}
	c.engineID = parsed.engineID
	c.boots, c.engineTime, c.timeAt = parsed.boots, parsed.engineTime, time.Now()
	c.authKey = localizeKey(c.masterKey, c.engineID)
	c.privKey = c.authKey[:16]
	return nil
}

// v3Message is the parts of an SNMPv3 answer.
type v3Message struct {
	msgID      int64
	flags      byte
	engineID   []byte
	boots      int64
	engineTime int64
	user       string
	// authStart is where the 12 bytes of the authentication code are in
	// the whole message.
	authParams []byte
	authStart  int
	privParams []byte
	data       []byte // the scoped PDU, or encrypted
	dataTag    byte
	dataStart  int
}

func parseV3(message []byte) (v3Message, error) {
	var m v3Message
	outer := &berReader{data: message}
	content, start, err := outer.expect(tagSequence)
	if err != nil {
		return m, err
	}
	r := inside(content, start)
	if version, err := r.integer(); err != nil || version != 3 {
		return m, fmt.Errorf("%w: not an SNMPv3 answer", errBER)
	}
	global, globalStart, err := r.expect(tagSequence)
	if err != nil {
		return m, err
	}
	g := inside(global, globalStart)
	if m.msgID, err = g.integer(); err != nil {
		return m, err
	}
	if _, err := g.integer(); err != nil {
		return m, err
	}
	flags, _, err := g.expect(tagOctetString)
	if err != nil || len(flags) != 1 {
		return m, errBER
	}
	m.flags = flags[0]
	security, securityStart, err := r.expect(tagOctetString)
	if err != nil {
		return m, err
	}
	sOuter := inside(security, securityStart)
	params, paramsStart, err := sOuter.expect(tagSequence)
	if err != nil {
		return m, err
	}
	s := inside(params, paramsStart)
	if m.engineID, _, err = s.expect(tagOctetString); err != nil {
		return m, err
	}
	if m.boots, err = s.integer(); err != nil {
		return m, err
	}
	if m.engineTime, err = s.integer(); err != nil {
		return m, err
	}
	user, _, err := s.expect(tagOctetString)
	if err != nil {
		return m, err
	}
	m.user = string(user)
	if m.authParams, m.authStart, err = s.expect(tagOctetString); err != nil {
		return m, err
	}
	if m.privParams, _, err = s.expect(tagOctetString); err != nil {
		return m, err
	}
	m.dataTag, m.data, m.dataStart, err = r.next()
	return m, err
}

// encodeV3 wraps a PDU into an SNMPv3 message, encrypted and authenticated
// with the user's keys.
func (c *snmpClient) encodeV3(pdu []byte, id int32) ([]byte, error) {
	boots, engineTime := c.boots, c.engineTime+int64(time.Since(c.timeAt)/time.Second)
	scoped := berSequence(tagSequence, berOctets(c.engineID), berOctets(nil), pdu)
	c.salt++
	salt := binary.BigEndian.AppendUint64(nil, c.salt)
	encrypted, err := aesCFB(c.privKey, boots, engineTime, salt, scoped, true)
	if err != nil {
		return nil, err
	}
	before := bytes.Join([][]byte{berOctets(c.engineID), berInteger(boots), berInteger(engineTime), berOctets([]byte(c.user))}, nil)
	params := bytes.Join([][]byte{before, berOctets(make([]byte, 12)), berOctets(salt)}, nil)
	security := berElement(tagSequence, params)
	// Where the 12 bytes of the authentication code start in security:
	// after its header, the parameters before them and their own header.
	within := len(security) - len(params) + len(before) + 2
	message := berSequence(tagSequence,
		berInteger(3),
		// Authenticated, encrypted and reportable.
		berSequence(tagSequence, berInteger(int64(id)), berInteger(snmpMaxMessage), berOctets([]byte{0x07}), berInteger(3)),
		berOctets(security),
		berOctets(encrypted),
	)
	at := bytes.Index(message, security)
	if at < 0 {
		return nil, errors.New("encode the SNMPv3 request")
	}
	copy(message[at+within:], authCode(c.authKey, message))
	return message, nil
}

// decodeV3 checks an SNMPv3 answer's authentication, decrypts it and reads
// its PDU. A report of the USM, which a router sends unauthenticated when
// it does not take a request, is read as it is.
func (c *snmpClient) decodeV3(message []byte, id int32) (byte, []varbind, error) {
	m, err := parseV3(message)
	if err != nil {
		return 0, nil, err
	}
	if m.msgID != int64(id) {
		return 0, nil, errors.New("the router answered another request")
	}
	scoped := m.data
	scopedStart := m.dataStart
	if m.flags&0x01 != 0 {
		if len(m.authParams) != 12 {
			return 0, nil, errBER
		}
		check := bytes.Clone(message)
		clear(check[m.authStart : m.authStart+12])
		if !hmac.Equal(authCode(c.authKey, check), m.authParams) {
			return 0, nil, errors.New("the SNMPv3 answer is not signed with the user's password")
		}
		c.boots, c.engineTime, c.timeAt = m.boots, m.engineTime, time.Now()
	}
	if m.flags&0x02 != 0 {
		if m.dataTag != tagOctetString {
			return 0, nil, errBER
		}
		scoped, err = aesCFB(c.privKey, m.boots, m.engineTime, m.privParams, m.data, false)
		if err != nil {
			return 0, nil, err
		}
		// Decrypted, the scoped PDU is a message of its own; its first
		// element is the sequence.
		r := &berReader{data: scoped}
		content, start, err := r.expect(tagSequence)
		if err != nil {
			return 0, nil, err
		}
		scoped, scopedStart = content, start
	} else if m.dataTag != tagSequence {
		return 0, nil, errBER
	}
	s := inside(scoped, scopedStart)
	if _, _, err := s.expect(tagOctetString); err != nil {
		return 0, nil, err
	}
	if _, _, err := s.expect(tagOctetString); err != nil {
		return 0, nil, err
	}
	tag, binds, err := decodePDU(s, id)
	if err != nil {
		return 0, nil, err
	}
	if tag == pduReport {
		for _, bind := range binds {
			// The clock is only taken from a report signed with the user's
			// key, as RFC 3414 sends it.
			if bind.oid == usmNotInTime && m.flags&0x01 != 0 {
				c.boots, c.engineTime, c.timeAt = m.boots, m.engineTime, time.Now()
			}
		}
	} else if m.flags&0x03 != 0x03 {
		// An answer to a request must be authenticated and encrypted, as
		// the request was.
		return 0, nil, errors.New("the SNMPv3 answer is not authenticated and encrypted")
	}
	return tag, binds, nil
}

// authCode is HMAC-SHA-96: the first 12 bytes of HMAC-SHA1.
func authCode(key, message []byte) []byte {
	mac := hmac.New(sha1.New, key)
	mac.Write(message)
	return mac.Sum(nil)[:12]
}

// aesCFB encrypts or decrypts with AES-128 in CFB mode as RFC 3826 does:
// the IV is the engine's boots and time and the 8 bytes of salt.
func aesCFB(key []byte, boots, engineTime int64, salt, data []byte, encrypt bool) ([]byte, error) {
	if len(salt) != 8 || len(key) < 16 {
		return nil, errors.New("the SNMPv3 encryption parameters are not valid")
	}
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		return nil, err
	}
	iv := binary.BigEndian.AppendUint32(nil, uint32(boots))    //nolint:gosec // 32 bits by the RFC
	iv = binary.BigEndian.AppendUint32(iv, uint32(engineTime)) //nolint:gosec // 32 bits by the RFC
	iv = append(iv, salt...)
	out := make([]byte, len(data))
	if encrypt {
		cipher.NewCFBEncrypter(block, iv).XORKeyStream(out, data) //nolint:staticcheck // CFB is what RFC 3826 requires
	} else {
		cipher.NewCFBDecrypter(block, iv).XORKeyStream(out, data) //nolint:staticcheck // CFB is what RFC 3826 requires
	}
	return out, nil
}
