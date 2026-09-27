package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestPortfolioWarningMessageLiveLikeSample checks that a portfolio warning is
// concise and does not turn a risk threshold alert into a manual-close request.
func TestPortfolioWarningMessageLiveLikeSample(t *testing.T) {
	cfgStrategies := []StrategyConfig{
		{ID: "hl-vwap-eth-60", Type: "perps", Args: []string{"--mode=live"}, MarginPerTradeUSD: ptrF(50)},
		{ID: "hl-rmc-eth-live", Type: "perps", Args: []string{"--mode=live"}, MarginPerTradeUSD: ptrF(50), Paused: true},
		{ID: "hl-tcross-eth-live", Type: "perps", Args: []string{"--mode=live"}, MarginPerTradeUSD: ptrF(50), Paused: true},
		{ID: "manual-eth", Type: "manual", Args: []string{"--mode=live"}, InitialCapital: 100, Capital: 100},
	}
	state := NewAppState()
	state.Strategies["hl-vwap-eth-60"] = &StrategyState{
		ID: "hl-vwap-eth-60", Platform: "hyperliquid", InitialCapital: 50,
		Positions: map[string]*Position{},
		RiskState: RiskState{CurrentDrawdownPct: 28.75, DailyPnL: -15},
	}
	state.Strategies["hl-rmc-eth-live"] = &StrategyState{
		ID: "hl-rmc-eth-live", Platform: "hyperliquid", InitialCapital: 220,
		Positions: map[string]*Position{},
		RiskState: RiskState{CurrentDrawdownPct: 0, DailyPnL: -220.28},
	}
	state.Strategies["hl-tcross-eth-live"] = &StrategyState{
		ID: "hl-tcross-eth-live", Platform: "hyperliquid", InitialCapital: 50,
		Positions: map[string]*Position{},
		RiskState: RiskState{CurrentDrawdownPct: 0, DailyPnL: -8.12},
	}
	state.Strategies["manual-eth"] = &StrategyState{
		ID: "manual-eth", Platform: "hyperliquid", InitialCapital: 100,
		Positions: map[string]*Position{},
		RiskState: RiskState{CurrentDrawdownPct: 0, DailyPnL: -39.36},
	}
	// Realistic live-equity state from the recent /status snapshot
	state.PortfolioRisk = map[RiskPartition]*PortfolioRiskState{
		livePartition: {
			PeakValue:                1014.25,
			CurrentDrawdownPct:       9.85,
			CurrentMarginDrawdownPct: 30.5,
			WarningSent:              true,
			WarnBandEnteredAt:        time.Now().UTC().Add(-30 * time.Minute),
			LastWarningMarginDDPct:   30.4,
			WarningMarginDeltaPct:    0.1,
		},
	}

	prices := map[string]float64{"ETH": 2700}
	msg := BuildPortfolioWarningMessage(PortfolioWarningMessageInputs{
		Reason:        "portfolio perps margin drawdown 30.5% exceeds limit 30.0%",
		Config:        &PortfolioRiskConfig{MaxDrawdownPct: 30, WarnThresholdPct: 100},
		State:         state,
		Partition:     livePartition,
		CfgStrategies: cfgStrategies,
		Prices:        prices,
		TotalValue:    913.7,
		PerpsMargin:   49.24,
		PerpsLoss:     15.02,
		Recent: []Trade{
			{StrategyID: "hl-vwap-eth-60", Side: "buy", Quantity: 0.361, Price: 2769.3, Timestamp: time.Now().Add(-10 * time.Minute), Details: "limit fill"},
		},
		Now:              time.Now().UTC(),
		EquityGuardArmed: true,
	})

	fmt.Println("===== BEGIN WARNING DM =====")
	fmt.Println(msg)
	fmt.Println("===== END WARNING DM =====")

	// Per-strategy triage and action advice belong to separate alerts.
	mustNotContain := []string{"hl-rmc-eth-live", "hl-tcross-eth-live", "Top contributors", "Recent activity", "Recommended:", "consider manually closing"}
	for _, s := range mustNotContain {
		if strings.Contains(msg, s) {
			t.Fatalf("warning unexpectedly contains %q: %s", s, msg)
		}
	}
	for _, want := range []string{"**PORTFOLIO WARNING LIVE**", "Warning threshold (equity or margin): 30.0%", "Equity kill switch: 30.0%", "no manual position close is requested", "per-strategy circuit breakers handle margin risk"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("warning missing %q: %s", want, msg)
		}
	}
}

func ptrF(v float64) *float64 { return &v }
