package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientIPTrustsHeadersOnlyFromProxy(t *testing.T) {
	cases := []struct{ remote, real, xff, want string }{
		{"203.0.113.7:5000", "1.2.3.4", "", "203.0.113.7"},      // internet client forging X-Real-IP
		{"203.0.113.7:5000", "", "1.2.3.4", "203.0.113.7"},      // … or X-Forwarded-For
		{"127.0.0.1:5000", "198.51.100.9", "", "198.51.100.9"},  // host nginx
		{"172.18.0.3:5000", "198.51.100.9", "", "198.51.100.9"}, // Caddy in the compose network
		{"172.18.0.3:5000", "", "198.51.100.9, 10.0.0.1", "198.51.100.9"},
		{"172.18.0.3:5000", "", "", "172.18.0.3"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.real != "" {
			r.Header.Set("X-Real-IP", c.real)
		}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(r); got != c.want {
			t.Errorf("%+v: got %s", c, got)
		}
	}
}

// Flooding unique keys must not reset the counters of keys that are still inside their window.
func TestLimiterSurvivesKeyFlood(t *testing.T) {
	l := newLimiter()
	for i := 0; i < 5; i++ {
		l.allow("otp|victim", 5, time.Minute)
	}
	for i := 0; i < 50001; i++ {
		l.allow("flood|"+string(rune(i)), 5, time.Minute)
	}
	if l.allow("otp|victim", 5, time.Minute) {
		t.Fatal("limit was reset by a flood of other keys")
	}
}
