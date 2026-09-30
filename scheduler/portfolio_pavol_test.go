package main

// Pavol fork: spot portfolio valuation with legacy multiplier.
// Kept across upstream merges (upstream #1597). See docs/GO-TRADER_OVERRIDES_PAVOL.md.

import (
	"testing"
)

func TestPortfolioValueSpotUsesAssetValueWithLegacyMultiplier(t *testing.T) {
	s := &StrategyState{
		Type: "spot",
		Cash: 0,
		Positions: map[string]*Position{
			"COTI": {Symbol: "COTI", Quantity: 10, AvgCost: 10, Side: "long", Multiplier: 1},
		},
		OptionPositions: make(map[string]*OptionPosition),
	}
	got := PortfolioValue(s, map[string]float64{"COTI": 8})
	if got != 80 {
		t.Fatalf("spot PortfolioValue = %.4f, want full marked inventory value 80", got)
	}
}
