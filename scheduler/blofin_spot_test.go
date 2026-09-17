package main

import "testing"

// BloFin spot strategies (bls-) are long-only: no direction field,
// EffectiveDirection must resolve to long and shorts must be disallowed.
func TestSpotEffectiveDirectionIsLongOnly(t *testing.T) {
	sc := StrategyConfig{Type: "spot", Platform: "blofin_spot"}
	if got := EffectiveDirection(sc); got != DirectionLong {
		t.Fatalf("spot EffectiveDirection = %v, want %v", got, DirectionLong)
	}
	if PerpsAllowsShort(sc) {
		t.Fatalf("spot PerpsAllowsShort = true, want false")
	}
	if !PerpsAllowsLong(sc) {
		t.Fatalf("spot PerpsAllowsLong = false, want true")
	}
}
