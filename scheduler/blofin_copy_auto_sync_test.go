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

func blofinSyncTestDB(t *testing.T) *StateDB {
	t.Helper()
	db, err := OpenStateDB(t.TempDir() + "/sync_test.db")
	if err != nil {
		t.Fatalf("OpenStateDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBookFundingResidualThreshold(t *testing.T) {
	db := blofinSyncTestDB(t)
	if got, _ := bookBloFinCopyFundingResidual(db, 351.0, 350.0, 1.0, 0.0, time.Now().UTC()); got != 0 {
		t.Fatalf("sub-threshold residual booked %v", got)
	}
	if total := blofinCopyAdjustmentsTotal(db); total != 0 {
		t.Fatalf("adjustments total=%v, want 0", total)
	}
	// (351.5-350.0) - 1.0 - 0.2 = 0.3 >= 0.10 -> booked.
	got, detail := bookBloFinCopyFundingResidual(db, 351.5, 350.0, 1.0, 0.2, time.Now().UTC())
	if diff := got - 0.3; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("residual=%v, want 0.3 (%s)", got, detail)
	}
	if total := blofinCopyAdjustmentsTotal(db); total-0.3 > 1e-9 || total-0.3 < -1e-9 {
		t.Fatalf("adjustments total=%v, want 0.3", total)
	}
	// Same window twice must not double-book (idempotent dedup).
	if got, _ := bookBloFinCopyFundingResidual(db, 351.5, 350.0, 1.0, 0.2, time.Now().UTC()); got != 0 {
		t.Fatalf("second booking=%v, want 0 (dedup)", got)
	}
}

func TestSyncStateRoundTrip(t *testing.T) {
	db := blofinSyncTestDB(t)
	st, err := loadBloFinCopySyncState(db)
	if err != nil || st.Found {
		t.Fatalf("fresh state found=%v err=%v", st.Found, err)
	}
	want := blofinCopySyncState{FillsSinceMs: 1790558257392, LastRunMs: 1790570000000, LastEquity: 351.27, LastLedger: 77.52, LastTradesNet: 77.52, LastAdjustments: -3.24, LastAccountNet: 74.28, Found: true}
	if err := storeBloFinCopySyncState(db, want); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err := loadBloFinCopySyncState(db)
	if err != nil || !got.Found {
		t.Fatalf("reload found=%v err=%v", got.Found, err)
	}
	if got.LastAccountNet-74.28 > 1e-9 || got.FillsSinceMs != want.FillsSinceMs {
		t.Fatalf("round trip mismatch: %+v", got)
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

func TestRealizedNetPnLForStrategy(t *testing.T) {
	db := blofinSyncTestDB(t)
	if _, err := db.db.Exec(`INSERT INTO trades (strategy_id,timestamp,symbol,side,quantity,price,value,is_close,realized_pnl,exchange_fee,pnl_gross,fee_source) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		"s1", "2026-09-25T00:00:00Z", "DOGE", "sell", 1, 1, 1, 1, 10.0, 0.5, 1, "userfills"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := db.RealizedNetPnLForStrategy("s1")
	if err != nil {
		t.Fatalf("net: %v", err)
	}
	if got-9.5 > 1e-9 || got-9.5 < -1e-9 {
		t.Fatalf("net=%v, want 9.5", got)
	}
	gross, err := db.RealizedPnLForStrategy("s1")
	if err != nil || gross != 10.0 {
		t.Fatalf("gross=%v err=%v, want 10.0", gross, err)
	}
}

func TestLiveBloFinOverviewShowsNetRealized(t *testing.T) {
	strategy := StrategyConfig{
		ID: "live-blofin-doge-1h", Type: "perps", Platform: "blofin",
		Args: []string{"stoch_rsi", "DOGE", "1h", "--mode=live"}, Capital: 100, InitialCapital: 100,
	}
	state := NewAppState()
	state.Strategies[strategy.ID] = &StrategyState{
		ID: strategy.ID, Type: strategy.Type, Platform: strategy.Platform,
		Cash: 100, InitialCapital: 100,
		Positions: map[string]*Position{}, OptionPositions: map[string]*OptionPosition{},
	}
	ss := newOpsTestServer(t, []StrategyConfig{strategy}, state, true)
	if _, err := ss.stateDB.primary().db.Exec(`INSERT INTO trades (
		strategy_id,timestamp,symbol,position_id,side,quantity,price,value,trade_type,
		is_close,realized_pnl,exchange_fee,pnl_gross,fee_source
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, strategy.ID, "2026-09-25T00:00:00Z", "DOGE", "p1", "sell", 1.0, 1.0, 1.0, "perps", 1, 10.0, 0.5, 1, "userfills"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	overview, _, ok := ss.uiStrategyOverviewWithPrices(strategy.ID, map[string]float64{})
	if !ok {
		t.Fatal("overview not found")
	}
	if overview.RealizedPnL-9.5 > 1e-9 || overview.RealizedPnL-9.5 < -1e-9 {
		t.Fatalf("overview realized=%v, want net 9.5", overview.RealizedPnL)
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
