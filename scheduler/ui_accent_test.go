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

func TestAccentStylesheet(t *testing.T) {
	css := []byte(":root{--bg:#fff;}")
	t.Setenv("DASHBOARD_ACCENT", "blue")
	out := accentStylesheet(css)
	if !strings.Contains(string(out), "--bg:#11151c") {
		t.Errorf("expected blue override appended")
	}
	t.Setenv("DASHBOARD_ACCENT", "")
	if got := accentStylesheet(css); string(got) != string(css) {
		t.Errorf("empty accent must leave css unchanged")
	}
}
