package main

// Pavol fork: risk/warning/force-close tests guarding fork overrides
// (BloFin live force-close, concise warning, spot multiplier).
// Kept across upstream merges — upstream removed most unit tests in #1597.
// See docs/GO-TRADER_OVERRIDES_PAVOL.md.

import (
	"strings"
	"testing"
	"time"
)
func TestForceCloseAllPositionsLiveBloFinPreservesExchangePosition(t *testing.T) {
	drainModelOnlyCloseAlerts()
	s := &StrategyState{
		ID:              "live-blofin-xlm-15m",
		Type:            "perps",
		Platform:        "blofin",
		Cash:            105,
		Positions:       map[string]*Position{"XLM": {Symbol: "XLM", Quantity: 34, AvgCost: 0.21759, Side: "long", Multiplier: 100}},
		OptionPositions: map[string]*OptionPosition{},
		TradeHistory:    []Trade{},
		ClosedPositions: []ClosedPosition{},
	}
	sc := &StrategyConfig{
		ID:       s.ID,
		Type:     "perps",
		Platform: "blofin",
		Args:     []string{"consolidation_range", "XLM", "15m", "--mode=live"},
	}
	forceCloseAllPositions(s, sc, map[string]float64{"XLM": 0.2161}, nil)

	if s.Positions["XLM"] == nil || s.Positions["XLM"].Quantity != 34 {
		t.Fatalf("live XLM position was changed by model-only force close: %+v", s.Positions["XLM"])
	}
	if len(s.TradeHistory) != 0 || len(s.ClosedPositions) != 0 {
		t.Fatalf("model-only live close wrote ledger rows: trades=%d closed=%d", len(s.TradeHistory), len(s.ClosedPositions))
	}
	if s.Cash != 105 {
		t.Fatalf("cash = %.4f, want unchanged 105", s.Cash)
	}
}

func TestForceCloseAllPositionsLiveBloFinPreservesPositionWithoutExchangeFill(t *testing.T) {
	s := &StrategyState{
		ID:              "live-blofin-xlm-15m",
		Type:            "perps",
		Platform:        "blofin",
		Cash:            105,
		Positions:       map[string]*Position{"XLM": {Symbol: "XLM", Quantity: 34, AvgCost: 0.21759, Side: "long", Multiplier: 100}},
		OptionPositions: map[string]*OptionPosition{},
		TradeHistory:    []Trade{},
		ClosedPositions: []ClosedPosition{},
	}
	sc := &StrategyConfig{
		ID:       s.ID,
		Type:     "perps",
		Platform: "blofin",
		Args:     []string{"consolidation_range", "XLM", "15m", "--mode=live"},
	}
	forceCloseAllPositions(s, sc, map[string]float64{"XLM": 0.2161}, nil)

	if pos := s.Positions["XLM"]; pos == nil || pos.Quantity != 34 || pos.Side != "long" {
		t.Fatalf("live position was changed without an exchange fill: %+v", pos)
	}
	if len(s.TradeHistory) != 0 || len(s.ClosedPositions) != 0 {
		t.Fatalf("model-only close wrote trade rows: trades=%d closed=%d", len(s.TradeHistory), len(s.ClosedPositions))
	}
	if s.Cash != 105 {
		t.Fatalf("cash = %.4f, want unchanged 105", s.Cash)
	}
}


func TestBuildPortfolioWarningMessage_IsConciseAndDoesNotRequestManualClose(t *testing.T) {
	now := time.Date(2026, 6, 6, 6, 5, 0, 0, time.UTC)
	state := &AppState{
		PortfolioRisk: map[RiskPartition]*PortfolioRiskState{livePartition: {
			PeakValue:                10060,
			CurrentDrawdownPct:       16.5,
			CurrentMarginDrawdownPct: 18.2,
			WarningSent:              true,
			WarnBandEnteredAt:        now.Add(-18 * time.Minute),
			WarningEquityDeltaPct:    1.2,
			WarningMarginDeltaPct:    0.8,
		}},
		Strategies: map[string]*StrategyState{
			"hl-btc-sma-30": {
				ID:             "hl-btc-sma-30",
				Type:           "perps",
				Cash:           1000,
				InitialCapital: 1500,
				RiskState:      RiskState{CurrentDrawdownPct: 9.1},
				Positions: map[string]*Position{
					"BTC": {Symbol: "BTC", Quantity: 0.5, AvgCost: 67800, Side: "short", Multiplier: 1},
				},
				OptionPositions: map[string]*OptionPosition{},
			},
			"hl-eth-ema-30": {
				ID:              "hl-eth-ema-30",
				Type:            "perps",
				Cash:            905,
				InitialCapital:  1000,
				RiskState:       RiskState{CurrentDrawdownPct: 4.1},
				Positions:       map[string]*Position{},
				OptionPositions: map[string]*OptionPosition{},
			},
		},
	}
	msg := BuildPortfolioWarningMessage(PortfolioWarningMessageInputs{
		Config:      &PortfolioRiskConfig{MaxDrawdownPct: 25, WarnThresholdPct: 60},
		State:       state,
		Partition:   livePartition,
		Prices:      map[string]float64{"BTC": 68080},
		TotalValue:  8400,
		PerpsLoss:   250,
		PerpsMargin: 1500,
		Recent: []Trade{
			{Timestamp: now.Add(-14 * time.Minute), StrategyID: "hl-btc-sma-30", Symbol: "BTC", Side: "sell", Quantity: 0.5, Price: 67800, TradeType: "perps", Details: "signal flip"},
		},
		Now:              now,
		EquityGuardArmed: true,
	})

	for _, want := range []string{
		"**PORTFOLIO WARNING LIVE**",
		"Equity drawdown: 16.5% ($8400 / peak $10060).",
		"Perps margin drawdown: 18.2% ($250 loss on $1500 margin).",
		"Warning threshold (equity or margin): 15.0%.",
		"Equity kill switch: 25.0%; current equity drawdown 16.5% (8.5 pp away).",
		"Perps margin drawdown: 18.2%.",
		"Heads-up only; no manual position close is requested.",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("warning message missing %q:\n%s", want, msg)
		}
	}
	for _, unwanted := range []string{"Top contributors:", "Recent activity", "Recommended:", "consider manually closing", "hl-btc-sma-30"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("concise warning unexpectedly contains %q:\n%s", unwanted, msg)
		}
	}
	if len(msg) >= 2000 {
		t.Fatalf("warning message len = %d, want under Discord limit; msg:\n%s", len(msg), msg)
	}
}

func TestBuildPortfolioWarningMessage_DoesNotListStrategyPnL(t *testing.T) {
	now := time.Date(2026, 6, 6, 6, 5, 0, 0, time.UTC)
	state := &AppState{
		PortfolioRisk: map[RiskPartition]*PortfolioRiskState{livePartition: {
			PeakValue:          1000,
			CurrentDrawdownPct: 20,
			WarningSent:        true,
			WarnBandEnteredAt:  now.Add(-5 * time.Minute),
		}},
		Strategies: map[string]*StrategyState{
			"no-initial-cap": {
				ID:              "no-initial-cap",
				Cash:            900,
				InitialCapital:  0,
				RiskState:       RiskState{DailyPnL: -75, CurrentDrawdownPct: 7.5},
				Positions:       map[string]*Position{},
				OptionPositions: map[string]*OptionPosition{},
			},
		},
	}
	msg := BuildPortfolioWarningMessage(PortfolioWarningMessageInputs{
		Config:           &PortfolioRiskConfig{MaxDrawdownPct: 25, WarnThresholdPct: 60},
		State:            state,
		TotalValue:       800,
		Now:              now,
		EquityGuardArmed: true,
	})
	if strings.Contains(msg, "daily P&L") || strings.Contains(msg, "no-initial-cap") {
		t.Fatalf("portfolio warning should not attribute risk to individual strategy P&L:\n%s", msg)
	}
}

func TestBuildPortfolioWarningMessage_DoesNotListPoolStrategyPnL(t *testing.T) {
	state := &AppState{Strategies: map[string]*StrategyState{
		"hl-pool": {
			ID: "hl-pool", Type: "perps",
			InitialCapital:              1000,
			SharedWalletPoolBudget:      true,
			SharedWalletPerformanceOnly: true,
			SharedWalletValueSet:        true,
			SharedWalletValue:           -75,
			Positions:                   map[string]*Position{},
			OptionPositions:             map[string]*OptionPosition{},
		},
	}}
	msg := BuildPortfolioWarningMessage(PortfolioWarningMessageInputs{
		Config:           &PortfolioRiskConfig{MaxDrawdownPct: 25, WarnThresholdPct: 60},
		State:            state,
		EquityGuardArmed: true,
	})
	for _, unwanted := range []string{"net P&L", "-$75", "-$1075", "hl-pool"} {
		if strings.Contains(msg, unwanted) {
			t.Fatalf("portfolio warning should not list strategy P&L %q:\n%s", unwanted, msg)
		}
	}
}


func TestForceCloseAllPositionsSpotCreditsSaleProceedsWithLegacyMultiplier(t *testing.T) {
	previousRecorder := tradeRecorder
	tradeRecorder = nil
	t.Cleanup(func() { tradeRecorder = previousRecorder })

	s := &StrategyState{
		ID:       "bls-spot-coti-1h",
		Type:     "spot",
		Platform: "blofin_spot",
		Cash:     0,
		Positions: map[string]*Position{
			"COTI": {Symbol: "COTI", Quantity: 10, AvgCost: 10, Side: "long", Multiplier: 1},
		},
		OptionPositions: make(map[string]*OptionPosition),
		TradeHistory:    []Trade{},
		ClosedPositions: []ClosedPosition{},
	}
	sc := &StrategyConfig{ID: s.ID, Type: "spot", Platform: "blofin_spot"}
	forceCloseAllPositions(s, sc, map[string]float64{"COTI": 8}, nil)

	if s.Cash != 80 {
		t.Fatalf("cash after spot model close = %.4f, want sale proceeds 80", s.Cash)
	}
	if len(s.Positions) != 0 || len(s.TradeHistory) != 1 || len(s.ClosedPositions) != 1 {
		t.Fatalf("spot model close state: positions=%d trades=%d closed=%d", len(s.Positions), len(s.TradeHistory), len(s.ClosedPositions))
	}
	trade := s.TradeHistory[0]
	if trade.Value != 80 || trade.RealizedPnL != -20 || !trade.IsClose {
		t.Fatalf("spot close trade = %+v, want value=80 gross PnL=-20 close=true", trade)
	}
}
