package main

import "testing"

func TestTopStepPaperRegimeRunsInlineNotInSharedStore(t *testing.T) {
	rc := &RegimeConfig{Enabled: true, Period: 14, ADXThreshold: 20}
	base := StrategyConfig{
		Type:     "futures",
		Platform: "topstep",
		Args:     []string{"ob_touch", "NQ", "5m", "--mode=paper"},
	}
	if req, ok := strategyRegimeBundleRequest(base, rc); ok {
		t.Fatalf("TopStep paper must compute regime in check_topstep, got shared request %+v", req)
	}

	base.Args[3] = "--mode=live"
	req, ok := strategyRegimeBundleRequest(base, rc)
	if !ok {
		t.Fatal("TopStep live must keep shared regime-store behavior")
	}
	if req.Key.Platform != "topstep" || req.Key.Symbol != "NQ" || req.Key.Timeframe != "5m" {
		t.Fatalf("live TopStep regime request = %+v", req.Key)
	}
}
