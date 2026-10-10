package router

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The BER tags SNMP uses.
const (
	tagInteger      = 0x02
	tagOctetString  = 0x04
	tagNull         = 0x05
	tagOID          = 0x06
	tagSequence     = 0x30
	tagIPAddress    = 0x40
	tagCounter32    = 0x41
	tagGauge32      = 0x42
	tagTimeTicks    = 0x43
	tagCounter64    = 0x46
	tagNoSuchObject = 0x80
	tagNoSuchInst   = 0x81
	tagEndOfMIB     = 0x82

	pduGet      = 0xa0
	pduResponse = 0xa2
	pduGetBulk  = 0xa5
	pduReport   = 0xa8
)

// berElement encodes one element: its tag, length and content.
func berElement(tag byte, content []byte) []byte {
	out := []byte{tag}
	out = append(out, berLength(len(content))...)
	return append(out, content...)
}

func berLength(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n & 0xff)}
	}
	var digits []byte
	for ; n > 0; n >>= 8 {
		digits = append([]byte{byte(n & 0xff)}, digits...)
	}
	return append([]byte{0x80 | byte(len(digits)&0x7f)}, digits...)
}

// berSequence encodes a sequence of encoded elements.
func berSequence(tag byte, elements ...[]byte) []byte {
	var content []byte
	for _, element := range elements {
		content = append(content, element...)
	}
	return berElement(tag, content)
}

// berInteger encodes a signed integer in as few bytes as it needs.
func berInteger(value int64) []byte {
	var content []byte
	for v := value; ; v >>= 8 {
		content = append([]byte{byte(v & 0xff)}, content...)
		if v >= -128 && v < 128 {
			break
		}
	}
	return berElement(tagInteger, content)
}

func berOctets(value []byte) []byte {
	return berElement(tagOctetString, value)
}

// berOID encodes an object identifier such as "1.3.6.1.2.1.1.3.0".
func berOID(oid string) ([]byte, error) {
	parts := strings.Split(strings.TrimPrefix(oid, "."), ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("the OID %q is too short", oid)
	}
	numbers := make([]uint64, len(parts))
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("the OID %q: %w", oid, err)
		}
		numbers[i] = n
	}
	if numbers[0] > 2 || (numbers[0] < 2 && numbers[1] >= 40) {
		return nil, fmt.Errorf("the OID %q does not start like one", oid)
	}
	content := berBase128(numbers[0]*40 + numbers[1])
	for _, n := range numbers[2:] {
		content = append(content, berBase128(n)...)
	}
	return berElement(tagOID, content), nil
}

func berBase128(n uint64) []byte {
	out := []byte{byte(n & 0x7f)}
	for n >>= 7; n > 0; n >>= 7 {
		out = append([]byte{byte(n&0x7f) | 0x80}, out...)
	}
	return out
}

// berReader reads elements from an encoding, keeping where each one starts
// in the whole message, which the authentication of SNMPv3 needs.
type berReader struct {
	data   []byte
	offset int
}

var errBER = errors.New("the answer is not valid BER")

// next reads one element and returns its tag, its content and where the
// content starts in the whole message.
func (r *berReader) next() (tag byte, content []byte, start int, err error) {
	if len(r.data) < 2 {
		return 0, nil, 0, errBER
	}
	tag = r.data[0]
	length := int(r.data[1])
	header := 2
	if length&0x80 != 0 {
		digits := length & 0x7f
		if digits == 0 || digits > 4 || len(r.data) < 2+digits {
			return 0, nil, 0, errBER
		}
		length = 0
		for _, b := range r.data[2 : 2+digits] {
			length = length<<8 | int(b)
		}
		header += digits
	}
	if length < 0 || len(r.data) < header+length {
		return 0, nil, 0, errBER
	}
	content = r.data[header : header+length]
	start = r.offset + header
	r.data = r.data[header+length:]
	r.offset += header + length
	return tag, content, start, nil
}

// expect reads one element with the given tag.
func (r *berReader) expect(tag byte) ([]byte, int, error) {
	got, content, start, err := r.next()
	if err != nil {
		return nil, 0, err
	}
	if got != tag {
		return nil, 0, fmt.Errorf("%w: got tag 0x%02x, want 0x%02x", errBER, got, tag)
	}
	return content, start, nil
}

// inside returns a reader of a constructed element's content, which starts
// at start in the whole message.
func inside(content []byte, start int) *berReader {
	return &berReader{data: content, offset: start}
}

func (r *berReader) integer() (int64, error) {
	content, _, err := r.expect(tagInteger)
	if err != nil {
		return 0, err
	}
	return parseInteger(content)
}

func parseInteger(content []byte) (int64, error) {
	if len(content) == 0 || len(content) > 8 {
		return 0, errBER
	}
	value := int64(content[0])
	if value >= 0x80 {
		value -= 0x100
	}
	for _, b := range content[1:] {
		value = value<<8 | int64(b)
	}
	return value, nil
}

func parseUnsigned(content []byte) (uint64, error) {
	if len(content) == 0 || len(content) > 9 || (len(content) == 9 && content[0] != 0) {
		return 0, errBER
	}
	var value uint64
	for _, b := range content {
		value = value<<8 | uint64(b)
	}
	return value, nil
}

func parseOID(content []byte) (string, error) {
	if len(content) == 0 {
		return "", errBER
	}
	var numbers []uint64
	var n uint64
	for i, b := range content {
		if n > 1<<56 {
			return "", errBER
		}
		n = n<<7 | uint64(b&0x7f)
		if b&0x80 != 0 {
			if i == len(content)-1 {
				return "", errBER
			}
			continue
		}
		if numbers == nil {
			first := min(n/40, 2)
			numbers = append(numbers, first, n-first*40)
		} else {
			numbers = append(numbers, n)
		}
		n = 0
	}
	parts := make([]string, len(numbers))
	for i, number := range numbers {
		parts[i] = strconv.FormatUint(number, 10)
	}
	return strings.Join(parts, "."), nil
}

// compareOIDs orders two OIDs as SNMP does, by their numbers.
func compareOIDs(a, b string) int {
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(x) && i < len(y); i++ {
		m, _ := strconv.ParseUint(x[i], 10, 64)
		n, _ := strconv.ParseUint(y[i], 10, 64)
		if m != n {
			if m < n {
				return -1
			}
			return 1
		}
	}
	return len(x) - len(y)
}

// snmpValue is the value of one variable in an answer.
type snmpValue struct {
	tag     byte
	number  uint64
	signed  int64
	text    string
	missing bool
}

// numeric returns the value as a number, for integers, counters, gauges and
// time ticks.
func (v snmpValue) numeric() (uint64, bool) {
	switch v.tag {
	case tagCounter32, tagGauge32, tagTimeTicks, tagCounter64:
		return v.number, true
	case tagInteger:
		if v.signed < 0 {
			return 0, false
		}
		return uint64(v.signed), true
	}
	return 0, false
}

func parseValue(tag byte, content []byte) (snmpValue, error) {
	value := snmpValue{tag: tag}
	var err error
	switch tag {
	case tagInteger:
		value.signed, err = parseInteger(content)
	case tagCounter32, tagGauge32, tagTimeTicks, tagCounter64:
		value.number, err = parseUnsigned(content)
	case tagOctetString:
		value.text = string(content)
	case tagOID:
		value.text, err = parseOID(content)
	case tagIPAddress:
		if len(content) == 4 {
			value.text = fmt.Sprintf("%d.%d.%d.%d", content[0], content[1], content[2], content[3])
		}
	case tagNull, tagNoSuchObject, tagNoSuchInst, tagEndOfMIB:
		value.missing = true
	}
	return value, err
}
