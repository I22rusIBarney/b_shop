package api

import (
	"html/template"
	"strings"
	"testing"
)

func TestAppLink(t *testing.T) {
	for to, want := range map[string]bool{
		"happ://add/https://x.example/sub/abc": true,
		"v2raytun://import/https://x":          true,
		"intent://x#Intent;scheme=a;end":       true,
		"https://evil.example":                 false,
		"JavaScript:alert(1)":                  false,
		"data:text/html,x":                     false,
		"not a link":                           false,
		"happ://a\nb":                          false,
	} {
		if _, ok := appLink(to); ok != want {
			t.Errorf("%q: got %v", to, ok)
		}
	}
	var b strings.Builder
	if err := openTpl.Execute(&b, template.URL(`happ://add/"><script>`)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), `"><script>`) {
		t.Fatal("href not escaped")
	}
}
