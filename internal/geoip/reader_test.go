package geoip

import (
	"net/netip"
	"testing"
)

func TestLookup(t *testing.T) {
	r, err := Open("testdata/mini.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cases := []struct {
		ip           string
		country, cty string
		region       string
		lat          float64
		ok           bool
	}{
		{"5.8.1.1", "RU", "Barnaul", "Altai Krai", 53.3548, true},
		{"85.1.200.3", "CH", "Zürich", "Zurich", 47.3769, true},
		{"177.1.2.3", "BR", "Curitiba", "Paraná", -25.4284, true},
		{"::ffff:5.8.9.9", "RU", "Barnaul", "Altai Krai", 53.3548, true}, // IPv4-mapped IPv6
		{"2001:db8::1", "DE", "", "", 51.0, true},
		{"8.8.8.8", "", "", "", 0, false},
		{"2001:4860::1", "", "", "", 0, false},
	}
	for _, c := range cases {
		l, ok := r.Lookup(netip.MustParseAddr(c.ip))
		if ok != c.ok {
			t.Fatalf("%s: ok=%v, want %v", c.ip, ok, c.ok)
		}
		if !ok {
			continue
		}
		if l.Country != c.country || l.City != c.cty || l.Region != c.region || !l.HasGeo || l.Lat < c.lat-0.001 || l.Lat > c.lat+0.001 {
			t.Errorf("%s: got %+v", c.ip, l)
		}
	}
}

func TestNotMMDB(t *testing.T) {
	if _, err := FromBytes([]byte("hello world, definitely not a database file at all")); err == nil {
		t.Fatal("expected an error")
	}
}
