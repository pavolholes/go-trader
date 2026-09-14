package main

import (
	"strings"
	"testing"
)

func TestDashboardAccentCSS(t *testing.T) {
	if got := dashboardAccentCSS(""); got != "" {
		t.Errorf("empty accent: expected no override, got %q", got)
	}
	if got := dashboardAccentCSS("green"); got != "" {
		t.Errorf("green accent: expected no override, got %q", got)
	}
	if got := dashboardAccentCSS("watermelon"); got != "" {
		t.Errorf("unknown accent: expected no override, got %q", got)
	}
	blue := dashboardAccentCSS(" BLUE ")
	if !strings.Contains(blue, "html.dark{--bg:#11151c") {
		t.Errorf("blue accent: missing dark bg remap")
	}
	if !strings.Contains(blue, ":root{--bg:#f2f4f8") {
		t.Errorf("blue accent: missing light bg remap")
	}
	if strings.Contains(blue, "--green:") || strings.Contains(blue, "--red:") {
		t.Errorf("blue accent: must not remap semantic colors")
	}
}

func TestInjectHeadStyle(t *testing.T) {
	html := []byte("<html><head><title>x</title></head><body></body></html>")
	out := injectHeadStyle(html, ":root{--bg:#111;}")
	s := string(out)
	if !strings.Contains(s, "<style id=dashboard-accent>:root{--bg:#111;}</style>") {
		t.Errorf("expected injected style block, got %s", s)
	}
	if strings.Index(s, "</style>") > strings.Index(s, "</head>") {
		t.Errorf("style must precede </head>")
	}
	plain := []byte("<html>no head here</html>")
	if got := string(injectHeadStyle(plain, "x")); got != string(plain) {
		t.Errorf("expected unchanged input, got %s", got)
	}
}
