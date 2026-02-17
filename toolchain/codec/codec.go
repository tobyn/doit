package codec

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/bits"
	"strconv"
	"strings"
)

// --- Base62 layer ---

var charToByte [256]byte
var byteToChar [62]byte

func init() {
	for i := range charToByte {
		charToByte[i] = 255
	}
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	for i := 0; i < 62; i++ {
		byteToChar[i] = alphabet[i]
		charToByte[alphabet[i]] = byte(i)
	}
}

func base62ReadU32(s string, pos int) (uint32, int) {
	var u uint32
	for pos < len(s) {
		c := s[pos]
		pos++
		b := charToByte[c]
		if b == 255 {
			if c <= 32 {
				continue
			}
			return 0, pos
		}
		u = u*31 + uint32(b%31)
		if b >= 31 {
			return u, pos
		}
	}
	return 0, pos
}

func base62WriteU32(buf *strings.Builder, u uint32) {
	var digits [10]byte
	n := 0
	digits[n] = byteToChar[31+u%31]
	n++
	u /= 31
	for u > 0 {
		digits[n] = byteToChar[u%31]
		n++
		u /= 31
	}
	for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	buf.Write(digits[:n])
}

func base62ReadData(s string, start, end int) ([]byte, error) {
	if start >= end {
		return nil, fmt.Errorf("no data to read")
	}
	idxChecksum := end - 1
	data := make([]byte, 0, ((idxChecksum-start)*4)/6)
	var chksum uint32

	idx := start
	for idx < idxChecksum {
		var b62bits uint64
		var count int
		for count < 6 && idx < idxChecksum {
			c := s[idx]
			idx++
			b := charToByte[c]
			if b == 255 {
				if c <= 32 {
					continue
				}
				return nil, fmt.Errorf("invalid character in base62 data")
			}
			b62bits = b62bits*62 + uint64(b)
			count++
		}
		chksum += uint32(b62bits)
		switch count {
		case 6:
			data = append(data, byte(b62bits&0xFF))
			b62bits >>= 8
			data = append(data, byte(b62bits&0xFF))
			b62bits >>= 8
			data = append(data, byte(b62bits&0xFF))
			b62bits >>= 8
			data = append(data, byte(b62bits&0xFF))
		case 5:
			data = append(data, byte(b62bits&0xFF))
			b62bits >>= 8
			data = append(data, byte(b62bits&0xFF))
			b62bits >>= 8
			data = append(data, byte(b62bits&0xFF))
		case 3:
			data = append(data, byte(b62bits&0xFF))
			b62bits >>= 8
			data = append(data, byte(b62bits&0xFF))
		case 2:
			data = append(data, byte(b62bits&0xFF))
		}
	}

	checksumByte := charToByte[s[idxChecksum]]
	if checksumByte != byte(chksum%62) {
		return nil, fmt.Errorf("base62 checksum mismatch")
	}
	return data, nil
}

func base62WriteData(buf *strings.Builder, data []byte) {
	charsForBytes := [4]int{0, 2, 3, 5}
	var chksum uint32

	for i := 0; i < len(data); i += 4 {
		remaining := len(data) - i
		var nchars int
		if remaining > 3 {
			nchars = 6
		} else {
			nchars = charsForBytes[remaining]
		}

		var b0, b1, b2, b3 byte
		b0 = data[i]
		if i+1 < len(data) {
			b1 = data[i+1]
		}
		if i+2 < len(data) {
			b2 = data[i+2]
		}
		if i+3 < len(data) {
			b3 = data[i+3]
		}

		b62bits := uint64(b0) | uint64(b1)<<8 | uint64(b2)<<16 | uint64(b3)<<24
		chksum += uint32(b62bits)

		var tok [6]byte
		tmp := b62bits
		for j := 0; j < nchars; j++ {
			tok[j] = byteToChar[tmp%62]
			tmp /= 62
		}
		for a, b := 0, nchars-1; a < b; a, b = a+1, b-1 {
			tok[a], tok[b] = tok[b], tok[a]
		}
		buf.Write(tok[:nchars])
	}

	buf.WriteByte(byteToChar[chksum%62])
}

// --- Compression layer ---

func zlibDecompress(data []byte) (out []byte, err error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("zlib decompress: %w", err)
	}
	defer func() {
		closeErr := r.Close()
		if err == nil {
			err = closeErr
		}
	}()
	out, err = io.ReadAll(r)
	if err != nil {
		err = fmt.Errorf("zlib decompress: %w", err)
	}
	return
}

func zlibCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, err := w.Write(data)
	closeErr := w.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("zlib compress: %w", err)
	}
	return buf.Bytes(), nil
}

// --- Binary format constants ---

const (
	mpFixMap   = 0x80
	mpFixArray = 0x90
	mpFixStr   = 0xa0
	mpNil      = 0xc0
	mpFalse    = 0xc2
	mpTrue     = 0xc3
	mpFloat32  = 0xca
	mpFloat64  = 0xcb
	mpUint8    = 0xcc
	mpUint16   = 0xcd
	mpUint32   = 0xce
	mpUint64   = 0xcf
	mpInt8     = 0xd0
	mpInt16    = 0xd1
	mpInt32    = 0xd2
	mpInt64    = 0xd3
	mpStr8     = 0xd9
	mpStr16    = 0xda
	mpStr32    = 0xdb
	mpArray16  = 0xdc
	mpArray32  = 0xdd
	mpMap16    = 0xde
	mpMap32    = 0xdf

	mpDesyncedDeadKey  = 0xc5
	mpDesyncedUserdata = 0xc1
)

// --- Binary reader ---

type reader struct {
	buf []byte
	pos int
}

func (r *reader) readByte() byte {
	b := r.buf[r.pos]
	r.pos++
	return b
}

func (r *reader) readUint16() uint16 {
	v := binary.LittleEndian.Uint16(r.buf[r.pos:])
	r.pos += 2
	return v
}

func (r *reader) readUint32() uint32 {
	v := binary.LittleEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v
}

func (r *reader) readUint64() uint64 {
	v := binary.LittleEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v
}

func (r *reader) readFloat64() float64 {
	return math.Float64frombits(r.readUint64())
}

func (r *reader) readIntPacked() int {
	var res, cnt int
	for {
		b := r.readByte()
		res |= int(b>>1) << (7 * cnt)
		cnt++
		if b&1 == 0 {
			break
		}
	}
	return res
}

func (r *reader) parse() any {
	typ := r.readByte()
	switch typ {
	case mpNil:
		return nil
	case mpFalse:
		return false
	case mpTrue:
		return true
	case mpFloat32:
		f := float64(math.Float32frombits(r.readUint32()))
		if n := int(f); float64(n) == f {
			return n
		}
		return f
	case mpFloat64:
		f := r.readFloat64()
		if n := int(f); float64(n) == f {
			return n
		}
		return f
	case mpUint8:
		return int(r.readByte())
	case mpUint16:
		return int(r.readUint16())
	case mpUint32:
		return int(r.readUint32())
	case mpUint64:
		return int(r.readUint64())
	case mpInt8:
		return int(int8(r.readByte()))
	case mpInt16:
		return int(int16(r.readUint16()))
	case mpInt32:
		return int(int32(r.readUint32()))
	case mpInt64:
		return int(int64(r.readUint64()))
	case mpStr8:
		n := int(r.readByte())
		s := string(r.buf[r.pos : r.pos+n])
		r.pos += n
		return s
	case mpStr16:
		n := int(r.readUint16())
		s := string(r.buf[r.pos : r.pos+n])
		r.pos += n
		return s
	case mpStr32:
		n := int(r.readUint32())
		s := string(r.buf[r.pos : r.pos+n])
		r.pos += n
		return s
	case mpArray16:
		return r.parseTable(int(r.readUint16()), false)
	case mpArray32:
		return r.parseTable(int(r.readUint32()), false)
	case mpMap16:
		return r.parseTable(int(r.readUint16()), true)
	case mpMap32:
		return r.parseTable(int(r.readUint32()), true)
	case mpDesyncedUserdata:
		udtype := r.readIntPacked()
		if udtype == 2 {
			ebits := r.readByte()
			e0 := ebits&4 != 0
			var eid, ever int
			if !e0 {
				eid = r.readIntPacked()
				if ebits&1 != 0 {
					eid = -eid
				}
				ever = r.readIntPacked()
				if ebits&2 != 0 {
					ever = -ever
				}
			}
			return fmt.Sprintf("__ENTITY:%d|%d__", eid, ever)
		}
		panic(fmt.Sprintf("parsing userdata type %d is not supported", udtype))
	default:
		switch {
		case typ < mpFixMap:
			return int(typ)
		case typ < mpFixArray:
			return r.parseTable(int(typ-mpFixMap), true)
		case typ < mpFixStr:
			return r.parseTable(int(typ-mpFixArray), false)
		case typ < mpNil:
			n := int(typ - mpFixStr)
			s := string(r.buf[r.pos : r.pos+n])
			r.pos += n
			return s
		case typ > mpMap32:
			return int(typ) - 256
		}
	}
	panic(fmt.Sprintf("cannot parse unknown type 0x%02x", typ))
}

// parseTable returns []any for array tables and map[string]any for map tables.
func (r *reader) parseTable(sz int, isMap bool) any {
	var sizeNode, sizeArray int
	if isMap {
		sizeNode = 1 << (sz >> 1)
		if sz&1 != 0 {
			sizeArray = r.readIntPacked()
		}
		r.readIntPacked() // lastfree
	} else {
		sizeArray = sz
	}

	var arr []any
	var m map[string]any
	if isMap {
		m = make(map[string]any)
	} else {
		arr = make([]any, sizeArray)
	}

	total := sizeArray + sizeNode
	for i := 0; i < total; {
		vacancyBits := r.readByte()
		groupEnd := i + 8
		if groupEnd > total {
			groupEnd = total
		}
		mask := byte(1)
		for ; i < groupEnd; i, mask = i+1, mask<<1 {
			if vacancyBits&mask != 0 {
				continue
			}
			val := r.parse()
			if i < sizeArray {
				if isMap {
					m[strconv.Itoa(i)] = val
				} else {
					arr[i] = val
				}
			} else {
				if r.buf[r.pos] == mpDesyncedDeadKey {
					r.pos++
					r.readIntPacked()
					continue
				}
				key := r.parse()
				var keyStr string
				switch k := key.(type) {
				case string:
					keyStr = k
				case int:
					keyStr = strconv.Itoa(k - 1)
				default:
					panic(fmt.Sprintf("unexpected table key type: %T", key))
				}
				m[keyStr] = val
				r.readIntPacked() // next
			}
		}
	}

	if isMap {
		return m
	}
	// Trim trailing nils (vacant Lua array slots beyond the last value).
	for len(arr) > 0 && arr[len(arr)-1] == nil {
		arr = arr[:len(arr)-1]
	}
	return arr
}

// --- Binary writer ---

type writer struct {
	buf []byte
}

func (w *writer) writeByte(b byte) {
	w.buf = append(w.buf, b)
}

func (w *writer) writeUint16(v uint16) {
	w.buf = append(w.buf, byte(v), byte(v>>8))
}

func (w *writer) writeUint32(v uint32) {
	w.buf = append(w.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func (w *writer) writeUint64(v uint64) {
	w.buf = append(w.buf,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

func (w *writer) writeFloat64(v float64) {
	w.writeUint64(math.Float64bits(v))
}

func (w *writer) writeIntPacked(v int) {
	for {
		b := v & 127
		v >>= 7
		if v != 0 {
			w.writeByte(byte(b<<1) | 1)
		} else {
			w.writeByte(byte(b << 1))
			return
		}
	}
}

func (w *writer) serialize(v any) {
	switch val := v.(type) {
	case nil:
		w.writeByte(mpNil)
	case bool:
		if val {
			w.writeByte(mpTrue)
		} else {
			w.writeByte(mpFalse)
		}
	case int:
		w.serializeInt(val)
	case float64:
		w.writeByte(mpFloat64)
		w.writeFloat64(val)
	case string:
		w.serializeString(val)
	case []any:
		w.serializeArray(val)
	case map[string]any:
		w.serializeMap(val)
	default:
		panic(fmt.Sprintf("cannot serialize type %T", v))
	}
}

func (w *writer) serializeInt(v int) {
	switch {
	case v > 0xffffffff:
		w.writeByte(mpUint64)
		w.writeUint64(uint64(v))
	case v > 0xffff:
		w.writeByte(mpUint32)
		w.writeUint32(uint32(v))
	case v > 0xff:
		w.writeByte(mpUint16)
		w.writeUint16(uint16(v))
	case v > 0x7f:
		w.writeByte(mpUint8)
		w.writeByte(byte(v))
	case v >= 0:
		w.writeByte(byte(v))
	case v >= -32:
		w.writeByte(byte(v + 256))
	case v >= -128:
		w.writeByte(mpInt8)
		w.writeByte(byte(int8(v)))
	case v >= -32768:
		w.writeByte(mpInt16)
		w.writeUint16(uint16(int16(v)))
	case v >= -2147483648:
		w.writeByte(mpInt32)
		w.writeUint32(uint32(int32(v)))
	default:
		w.writeByte(mpInt64)
		w.writeUint64(uint64(v))
	}
}

func (w *writer) serializeString(v string) {
	if eid, ever, ok := parseEntity(v); ok {
		w.writeByte(mpDesyncedUserdata)
		w.writeIntPacked(2)
		e0 := eid == 0 && ever == 0
		var ebits byte
		if eid < 0 {
			ebits |= 1
		}
		if ever < 0 {
			ebits |= 2
		}
		if e0 {
			ebits |= 4
		}
		w.writeByte(ebits)
		if !e0 {
			w.writeIntPacked(intAbs(eid))
			w.writeIntPacked(intAbs(ever))
		}
		return
	}

	strsz := len(v)
	switch {
	case strsz < 32:
		w.writeByte(byte(mpFixStr) | byte(strsz))
	case strsz < 256:
		w.writeByte(mpStr8)
		w.writeByte(byte(strsz))
	case strsz < 65536:
		w.writeByte(mpStr16)
		w.writeUint16(uint16(strsz))
	default:
		w.writeByte(mpStr32)
		w.writeUint32(uint32(strsz))
	}
	w.buf = append(w.buf, v...)
}

func (w *writer) serializeArray(arr []any) {
	sz := len(arr)
	if sz < 16 {
		w.writeByte(byte(mpFixArray) | byte(sz))
	} else if sz < 65536 {
		w.writeByte(mpArray16)
		w.writeUint16(uint16(sz))
	} else {
		w.writeByte(mpArray32)
		w.writeUint32(uint32(sz))
	}

	for i := 0; i < sz; {
		groupEnd := i + 8
		if groupEnd > sz {
			groupEnd = sz
		}
		vacancyPos := len(w.buf)
		w.writeByte(0)
		var vacancyBits byte
		for bit := 0; i < groupEnd; i, bit = i+1, bit+1 {
			if arr[i] == nil {
				vacancyBits |= 1 << bit
				continue
			}
			w.serialize(arr[i])
		}
		w.buf[vacancyPos] = vacancyBits
	}
}

func (w *writer) serializeMap(m map[string]any) {
	// Determine array part: contiguous keys from "0", allowing 1 gap.
	sizeArray := 0
	arrayKeys := 0
	for {
		if _, ok := m[strconv.Itoa(sizeArray)]; ok {
			arrayKeys++
		} else if _, ok := m[strconv.Itoa(sizeArray+1)]; !ok {
			break
		}
		sizeArray++
	}

	mapKeys := len(m) - arrayKeys

	bitLen := 0
	if mapKeys > 0 {
		bitLen = 1
		if mapKeys > 1 {
			bitLen = bits.Len(uint(mapKeys - 1))
		}
	}
	sizeNode := 1 << bitLen

	sz := bitLen << 1
	if sizeArray > 0 {
		sz |= 1
	}

	if sz < 16 {
		w.writeByte(byte(mpFixMap) | byte(sz))
	} else if sz < 65536 {
		w.writeByte(mpMap16)
		w.writeUint16(uint16(sz))
	} else {
		w.writeByte(mpMap32)
		w.writeUint32(uint32(sz))
	}

	if sizeArray > 0 {
		w.writeIntPacked(sizeArray)
	}

	hashKeys := make([]string, 0, mapKeys)
	for k := range m {
		n, err := strconv.Atoi(k)
		if err != nil || n < 0 || n >= sizeArray {
			hashKeys = append(hashKeys, k)
		}
	}

	w.writeIntPacked(0) // lastfree

	total := sizeArray + sizeNode
	last := sizeArray + mapKeys

	for i := 0; i < total; {
		groupEnd := i + 8
		if groupEnd > total {
			groupEnd = total
		}
		vacancyPos := len(w.buf)
		w.writeByte(0)
		var vacancyBits byte
		for bit := 0; i < groupEnd; i, bit = i+1, bit+1 {
			if i >= last {
				vacancyBits |= 1 << bit
				continue
			}
			if i < sizeArray {
				val, ok := m[strconv.Itoa(i)]
				if !ok {
					vacancyBits |= 1 << bit
					continue
				}
				w.serialize(val)
			} else {
				key := hashKeys[i-sizeArray]
				w.serialize(m[key])
				if isNumericKey(key) {
					n, _ := strconv.Atoi(key)
					w.serialize(n + 1)
				} else {
					w.serialize(key)
				}
				w.writeIntPacked(0) // next
			}
		}
		w.buf[vacancyPos] = vacancyBits
	}
}

// --- Public API ---

// ObjectType identifies the kind of Desynced object.
type ObjectType byte

const (
	Unknown   ObjectType = 0
	Blueprint ObjectType = 'B'
	Behavior  ObjectType = 'C'
)

func (t ObjectType) String() string {
	switch t {
	case Blueprint:
		return "Blueprint"
	case Behavior:
		return "Behavior"
	case Unknown:
		return "Unknown"
	default:
		return fmt.Sprintf("ObjectType(%d)", t)
	}
}

// Object is a decoded Desynced object with its type indicator.
type Object struct {
	Type  ObjectType
	Value any
}

// Decode reads a Base62-encoded string from r and decodes it.
func Decode(r io.Reader) (*Object, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return DecodeString(strings.TrimSpace(string(data)))
}

// DecodeString decodes a Base62-encoded string.
func DecodeString(s string) (obj *Object, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("decode: %v", r)
		}
	}()

	if len(s) < 5 {
		return nil, fmt.Errorf("input string is too short")
	}
	if s[0] != 'D' || s[1] != 'S' {
		return nil, fmt.Errorf("input string does not begin with 'DS'")
	}

	decompressLen, pos := base62ReadU32(s, 3)

	data, err := base62ReadData(s, pos, len(s))
	if err != nil {
		return nil, err
	}

	if decompressLen > 0 {
		data, err = zlibDecompress(data)
		if err != nil {
			return nil, err
		}
	}

	r := &reader{buf: data}
	return &Object{Type: ObjectType(s[2]), Value: r.parse()}, nil
}

// Encode encodes an Object and writes the Base62 string to w.
func Encode(w io.Writer, obj *Object) error {
	s, err := EncodeString(obj)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, s)
	return err
}

// EncodeString encodes an Object as a Base62 string.
func EncodeString(obj *Object) (s string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("encode: %v", r)
		}
	}()

	wr := &writer{}
	wr.serialize(obj.Value)
	data := wr.buf

	compressed, cerr := zlibCompress(data)
	var decompressLen uint32
	if cerr == nil && len(compressed) < len(data) {
		decompressLen = uint32(len(data))
		data = compressed
	}

	var buf strings.Builder
	buf.WriteString("DS")
	buf.WriteByte(byte(obj.Type))
	base62WriteU32(&buf, decompressLen)
	base62WriteData(&buf, data)
	return buf.String(), nil
}

// --- Helpers ---

func isNumericKey(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func parseEntity(s string) (eid, ever int, ok bool) {
	if !strings.HasPrefix(s, "__ENTITY:") || !strings.HasSuffix(s, "__") {
		return 0, 0, false
	}
	inner := s[9 : len(s)-2]
	parts := strings.SplitN(inner, "|", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	var err error
	eid, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	ever, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return eid, ever, true
}

func intAbs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
