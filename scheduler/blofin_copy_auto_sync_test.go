package main

import (
	"testing"
	"time"
)

func blofinSyncTestConfig() []StrategyConfig {
	return []StrategyConfig{
		{ID: "live-stoch_rsi-doge-1h", Type: "perps", Platform: "blofin", Args: []string{"stoch_rsi", "DOGE", "1h", "--mode=live"}},
		{ID: "live-parabolic_sar-eth-15m", Type: "perps", Platform: "blofin", Args: []string{"parabolic_sar", "ETH", "15m", "--mode=live"}},
		{ID: "live-heikin_ashi_ema-eth-30m", Type: "perps", Platform: "blofin", Args: []string{"heikin_ashi_ema", "ETH", "30m", "--mode=live"}},
	}
}

func blofinSyncTestState() *AppState {
	state := NewAppState()
	state.Strategies["live-stoch_rsi-doge-1h"] = &StrategyState{
		ID: "live-stoch_rsi-doge-1h", Type: "perps", Platform: "blofin",
		Cash: 1000, InitialCapital: 1000,
		Positions: map[string]*Position{}, TradeHistory: []Trade{},
	}
	return state
}

func blofinSyncTestPayload() *blofinCopySyncPayload {
	return &blofinCopySyncPayload{
		Incremental: true,
		SinceMs:     1790000000000,
		Positions: []blofinCopySyncPosition{
			{
				OrderID: "16780203", InstID: "DOGE-USDT", Symbol: "DOGE",
				Side: "buy", PositionSide: "long", Quantity: "1",
				OpenPrice: "0.09967", OpenMs: 1790114620823, CloseMs: 1790114664455,
				ClosePrice: "0.09963", RealizedPnL: "-0.04", CloseType: "close",
				ContractValue: "1000",
				Closes: []blofinCopySyncClose{
					{CloseOrderID: "7327495", Side: "sell", Size: "1", AveragePrice: "0.09963", Fee: "0.059778", RealizedPnl: "-0.04", OrderTime: 1790114664455},
				},
			},
		},
		Orders: map[string]blofinCopySyncOrder{
			"16780203": {OrderID: "16780203", InstID: "DOGE-USDT", Side: "buy", FilledSize: "1", AveragePrice: "0.09967", Fee: "0.059802", CreateTime: 1790114620823},
			"16780216": {OrderID: "16780216", InstID: "DOGE-USDT", Side: "sell", FilledSize: "1", AveragePrice: "0.09963", Fee: "0.059778", Pnl: "-0.04", CreateTime: 1790114664455},
		},
	}
}

func TestMatchBloFinCopyStrategyDirect(t *testing.T) {
	state := blofinSyncTestState()
	state.Strategies["live-stoch_rsi-doge-1h"].TradeHistory = []Trade{
		{StrategyID: "live-stoch_rsi-doge-1h", Symbol: "DOGE", Side: "buy", ExchangeOrderID: "16780203"},
	}
	got, err := matchBloFinCopyStrategy("16780203", "DOGE", "buy", 1790114620823, state, map[string][]string{"DOGE": {"live-stoch_rsi-doge-1h"}})
	if err != nil || got != "live-stoch_rsi-doge-1h" {
		t.Fatalf("direct match = %q, %v", got, err)
	}
}

func TestMatchBloFinCopyStrategySingleSymbol(t *testing.T) {
	state := blofinSyncTestState()
	got, err := matchBloFinCopyStrategy("999", "DOGE", "buy", 1790114620823, state, map[string][]string{"DOGE": {"live-stoch_rsi-doge-1h"}})
	if err != nil || got != "live-stoch_rsi-doge-1h" {
		t.Fatalf("single-symbol match = %q, %v", got, err)
	}
}

func TestMatchBloFinCopyStrategyAmbiguous(t *testing.T) {
	state := blofinSyncTestState()
	_, err := matchBloFinCopyStrategy("999", "ETH", "buy", 1790114620823, state,
		map[string][]string{"ETH": {"live-parabolic_sar-eth-15m", "live-heikin_ashi_ema-eth-30m"}})
	if err == nil {
		t.Fatal("expected ambiguity error for shared ETH symbol")
	}
}

func TestApplyInsertsMissingDogePosition(t *testing.T) {
	state := blofinSyncTestState()
	now := time.UnixMilli(1790570000000).UTC()
	out, err := applyBloFinCopySyncPayload(state, blofinSyncTestConfig(), blofinSyncTestPayload(), now)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out.PositionsInserted != 1 || out.TradesInserted != 2 {
		t.Fatalf("inserted positions=%d trades=%d, want 1/2 (skipped=%+v)", out.PositionsInserted, out.TradesInserted, out.Skipped)
	}
	ss := state.Strategies["live-stoch_rsi-doge-1h"]
	if len(ss.ClosedPositions) != 1 {
		t.Fatalf("closed positions=%d, want 1", len(ss.ClosedPositions))
	}
	// Ledger: open -0.059802 + close (-0.04-0.059778) = -0.15958.
	wantLedger := -0.059802 + (-0.04 - 0.059778)
	if diff := out.LedgerDelta - wantLedger; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("ledger delta=%v, want %v", out.LedgerDelta, wantLedger)
	}
	if diff := ss.Cash - (1000 + wantLedger); diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("cash=%v, want %v", ss.Cash, 1000+wantLedger)
	}
	if ss.ClosedPositions[0].RealizedPnL-(-0.04-0.059778) > 1e-9 {
		t.Fatalf("closed realized=%v", ss.ClosedPositions[0].RealizedPnL)
	}

	// Second apply must be a no-op (idempotent by exchange order ID).
	out2, err := applyBloFinCopySyncPayload(state, blofinSyncTestConfig(), blofinSyncTestPayload(), now)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if out2.PositionsInserted != 0 || out2.TradesInserted != 0 {
		t.Fatalf("re-apply inserted %d/%d, want 0/0", out2.PositionsInserted, out2.TradesInserted)
	}
}

func TestBookFundingResidualThreshold(t *testing.T) {
	state := NewAppState()
	if got, _ := bookBloFinCopyFundingResidual(state, 351.0, 350.0, 1.0, 0.0, time.Now().UTC()); got != 0 {
		t.Fatalf("sub-threshold residual booked %v", got)
	}
	// (351.5-350.0) - 1.0 - 0.2 = 0.3 >= 0.10 -> booked.
	got, detail := bookBloFinCopyFundingResidual(state, 351.5, 350.0, 1.0, 0.2, time.Now().UTC())
	if diff := got - 0.3; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("residual=%v, want 0.3 (%s)", got, detail)
	}
	ss := state.Strategies[blofinCopyFundingStrategyID]
	if ss == nil || len(ss.TradeHistory) != 1 {
		t.Fatalf("funding book missing")
	}
	tr := ss.TradeHistory[0]
	if tr.TradeType != TradeTypeFunding || !tr.PnLGross {
		t.Fatalf("funding row type=%q gross=%v", tr.TradeType, tr.PnLGross)
	}
}

func TestBloFinCopySyncDue(t *testing.T) {
	now := time.Now().UTC()
	if !blofinCopySyncDue(0, now) {
		t.Fatal("first run should be due")
	}
	if !blofinCopySyncDue(now.Add(-7*time.Hour).UnixMilli(), now) {
		t.Fatal("stale run should be due")
	}
	if blofinCopySyncDue(now.Add(-1*time.Hour).UnixMilli(), now) {
		t.Fatal("fresh run should not be due")
	}
}

func TestMatchBloFinCopyOrder(t *testing.T) {
	orders := blofinSyncTestPayload().Orders
	got, err := matchBloFinCopyOrder(orders, "DOGE-USDT", 1790114664455, "sell", 1, 0.09963)
	if err != nil || got.OrderID != "16780216" {
		t.Fatalf("close match = %+v, %v", got, err)
	}
	if _, err := matchBloFinCopyOrder(orders, "DOGE-USDT", 1790114664455, "sell", 2, 0.09963); err == nil {
		t.Fatal("expected no match for wrong quantity")
	}
}
