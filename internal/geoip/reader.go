// Package geoip looks up the country and city of an IP address in an MMDB database (DB-IP "City Lite",
// MaxMind GeoLite2 and compatible files). It is a small read-only implementation of the MMDB format
// (https://maxmind.github.io/MaxMind-DB/) so the shop needs no extra dependencies.
package geoip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/netip"
)

// Location is what the shop keeps about an address. Names are in English, as in the database.
type Location struct {
	Country string // ISO 3166-1 alpha-2, "" when unknown
	Region  string // first subdivision (state, krai…)
	City    string
	Lat     float64
	Lon     float64
	HasGeo  bool // Lat/Lon are real coordinates
}

// Lookuper is implemented by *Reader; the service depends on this so tests can substitute a fake.
type Lookuper interface {
	Lookup(ip netip.Addr) (Location, bool)
}

type Reader struct {
	buf        []byte
	data       []byte // data section
	nodeCount  uint32
	recordSize uint32
	nodeBytes  uint32
	treeSize   uint32
	ipVersion  uint32
	ipv4Start  uint32
	closer     func() error
}

var metaMarker = []byte("\xAB\xCD\xEFMaxMind.com")

// Open reads the database at path.
func Open(path string) (*Reader, error) {
	buf, closer, err := readFile(path)
	if err != nil {
		return nil, err
	}
	r, err := newReader(buf)
	if err != nil {
		if closer != nil {
			_ = closer()
		}
		return nil, err
	}
	r.closer = closer
	return r, nil
}

// FromBytes builds a reader over an in-memory database.
func FromBytes(buf []byte) (*Reader, error) { return newReader(buf) }

func (r *Reader) Close() error {
	if r.closer != nil {
		return r.closer()
	}
	return nil
}

func newReader(buf []byte) (*Reader, error) {
	i := bytes.LastIndex(buf, metaMarker)
	if i < 0 {
		return nil, errors.New("geoip: not an MMDB file")
	}
	meta := buf[i+len(metaMarker):]
	d := decoder{buf: meta}
	v, _, err := d.decode(0)
	if err != nil {
		return nil, fmt.Errorf("geoip: metadata: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("geoip: bad metadata")
	}
	num := func(k string) uint32 {
		switch x := m[k].(type) {
		case uint64:
			return uint32(x)
		case int32:
			return uint32(x)
		}
		return 0
	}
	r := &Reader{buf: buf, nodeCount: num("node_count"), recordSize: num("record_size"), ipVersion: num("ip_version")}
	if r.nodeCount == 0 || (r.recordSize != 24 && r.recordSize != 28 && r.recordSize != 32) || (r.ipVersion != 4 && r.ipVersion != 6) {
		return nil, errors.New("geoip: unsupported database")
	}
	r.nodeBytes = r.recordSize / 4
	r.treeSize = r.nodeCount * r.nodeBytes
	if int(r.treeSize)+16 > i {
		return nil, errors.New("geoip: truncated database")
	}
	r.data = buf[r.treeSize+16 : i]
	if r.ipVersion == 6 {
		node := uint32(0)
		for k := 0; k < 96 && node < r.nodeCount; k++ {
			node = r.read(node, 0)
		}
		r.ipv4Start = node
	}
	return r, nil
}

func (r *Reader) read(node uint32, bit byte) uint32 {
	o := node * r.nodeBytes
	b := r.buf[o : o+r.nodeBytes]
	switch r.recordSize {
	case 24:
		if bit == 0 {
			return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
		}
		return uint32(b[3])<<16 | uint32(b[4])<<8 | uint32(b[5])
	case 28:
		if bit == 0 {
			return uint32(b[3]&0xF0)<<20 | uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
		}
		return uint32(b[3]&0x0F)<<24 | uint32(b[4])<<16 | uint32(b[5])<<8 | uint32(b[6])
	default:
		if bit == 0 {
			return binary.BigEndian.Uint32(b[0:4])
		}
		return binary.BigEndian.Uint32(b[4:8])
	}
}

// Lookup finds the record for ip. ok is false for addresses missing from the database.
func (r *Reader) Lookup(ip netip.Addr) (Location, bool) {
	ip = ip.Unmap()
	var raw []byte
	node := uint32(0)
	if ip.Is4() {
		a := ip.As4()
		raw = a[:]
		if r.ipVersion == 6 {
			node = r.ipv4Start
		}
	} else {
		if r.ipVersion == 4 {
			return Location{}, false
		}
		a := ip.As16()
		raw = a[:]
	}
	for i := 0; i < len(raw)*8 && node < r.nodeCount; i++ {
		node = r.read(node, (raw[i>>3]>>(7-uint(i&7)))&1)
	}
	if node <= r.nodeCount {
		return Location{}, false
	}
	off := node - r.nodeCount - 16
	if int(off) >= len(r.data) {
		return Location{}, false
	}
	d := decoder{buf: r.data}
	v, _, err := d.decode(int(off))
	if err != nil {
		return Location{}, false
	}
	m, _ := v.(map[string]any)
	if m == nil {
		return Location{}, false
	}
	return locationOf(m), true
}

func locationOf(m map[string]any) Location {
	var l Location
	if c, ok := m["country"].(map[string]any); ok {
		l.Country, _ = c["iso_code"].(string)
	}
	if l.Country == "" {
		if c, ok := m["registered_country"].(map[string]any); ok {
			l.Country, _ = c["iso_code"].(string)
		}
	}
	if c, ok := m["city"].(map[string]any); ok {
		l.City = englishName(c)
	}
	if s, ok := m["subdivisions"].([]any); ok && len(s) > 0 {
		if sm, ok := s[0].(map[string]any); ok {
			l.Region = englishName(sm)
		}
	}
	if loc, ok := m["location"].(map[string]any); ok {
		la, ok1 := loc["latitude"].(float64)
		lo, ok2 := loc["longitude"].(float64)
		if ok1 && ok2 && !math.IsNaN(la) && !math.IsNaN(lo) {
			l.Lat, l.Lon, l.HasGeo = la, lo, true
		}
	}
	return l
}

func englishName(m map[string]any) string {
	if n, ok := m["names"].(map[string]any); ok {
		if s, ok := n["en"].(string); ok {
			return s
		}
	}
	return ""
}

// ---- data section decoder ----

type decoder struct{ buf []byte }

var errTrunc = errors.New("truncated data")

// decode reads one value at off and returns it with the offset after it.
func (d *decoder) decode(off int) (any, int, error) {
	return d.decodeDepth(off, 0)
}

func (d *decoder) decodeDepth(off, depth int) (any, int, error) {
	if depth > 16 {
		return nil, 0, errors.New("too deep")
	}
	if off >= len(d.buf) {
		return nil, 0, errTrunc
	}
	ctrl := d.buf[off]
	off++
	typ := int(ctrl >> 5)
	if typ == 1 { // pointer
		ss := int(ctrl>>3) & 3
		vvv := int(ctrl & 7)
		n := ss + 1
		if ss == 3 {
			n = 4
		}
		if off+n > len(d.buf) {
			return nil, 0, errTrunc
		}
		var p int
		switch ss {
		case 0:
			p = vvv<<8 | int(d.buf[off])
		case 1:
			p = (vvv<<16 | int(d.buf[off])<<8 | int(d.buf[off+1])) + 2048
		case 2:
			p = (vvv<<24 | int(d.buf[off])<<16 | int(d.buf[off+1])<<8 | int(d.buf[off+2])) + 526336
		default:
			p = int(binary.BigEndian.Uint32(d.buf[off : off+4]))
		}
		v, _, err := d.decodeDepth(p, depth+1)
		return v, off + n, err
	}
	if typ == 0 { // extended type
		if off >= len(d.buf) {
			return nil, 0, errTrunc
		}
		typ = 7 + int(d.buf[off])
		off++
	}
	size := int(ctrl & 31)
	if size >= 29 {
		extra := size - 28
		if off+extra > len(d.buf) {
			return nil, 0, errTrunc
		}
		n := 0
		for i := 0; i < extra; i++ {
			n = n<<8 | int(d.buf[off+i])
		}
		off += extra
		switch size {
		case 29:
			size = 29 + n
		case 30:
			size = 285 + n
		default:
			size = 65821 + n
		}
	}
	if typ == 14 { // boolean: the value lives in the size
		return size != 0, off, nil
	}
	if typ == 7 { // map
		m := make(map[string]any, size)
		for i := 0; i < size; i++ {
			k, next, err := d.decodeDepth(off, depth+1)
			if err != nil {
				return nil, 0, err
			}
			ks, ok := k.(string)
			if !ok {
				return nil, 0, errors.New("map key is not a string")
			}
			v, next2, err := d.decodeDepth(next, depth+1)
			if err != nil {
				return nil, 0, err
			}
			m[ks] = v
			off = next2
		}
		return m, off, nil
	}
	if typ == 11 { // array
		a := make([]any, 0, size)
		for i := 0; i < size; i++ {
			v, next, err := d.decodeDepth(off, depth+1)
			if err != nil {
				return nil, 0, err
			}
			a = append(a, v)
			off = next
		}
		return a, off, nil
	}
	if off+size > len(d.buf) {
		return nil, 0, errTrunc
	}
	b := d.buf[off : off+size]
	off += size
	switch typ {
	case 2:
		return string(b), off, nil
	case 3:
		if size != 8 {
			return nil, 0, errors.New("bad double")
		}
		return math.Float64frombits(binary.BigEndian.Uint64(b)), off, nil
	case 15:
		if size != 4 {
			return nil, 0, errors.New("bad float")
		}
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), off, nil
	case 4:
		return append([]byte(nil), b...), off, nil
	case 5, 6, 9, 10: // unsigned integers (uint128 is truncated to its low 64 bits)
		var n uint64
		for _, c := range b {
			n = n<<8 | uint64(c)
		}
		return n, off, nil
	case 8:
		var n uint32
		for _, c := range b {
			n = n<<8 | uint32(c)
		}
		return int32(n), off, nil
	case 12, 13:
		return nil, off, nil
	}
	return nil, 0, fmt.Errorf("unknown type %d", typ)
}
