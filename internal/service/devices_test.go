package service

import "testing"

func TestAppFromUserAgent(t *testing.T) {
	cases := map[string]string{
		"Happ/3.7.0/ios":        "Happ",
		"INCY/3.7.0/android/14": "INCY",
		"v2rayNG/1.9.30":        "v2rayNG",
		"Streisand 1.6":         "Streisand",
		"ClashMeta (Windows)":   "ClashMeta",
		"":                      "",
		"   ":                   "",
	}
	for ua, want := range cases {
		if got := AppFromUserAgent(ua); got != want {
			t.Errorf("%q: got %q, want %q", ua, got, want)
		}
	}
}
