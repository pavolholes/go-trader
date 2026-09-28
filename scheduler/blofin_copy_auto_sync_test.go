package main

import (
	"math"
	"strings"
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
	out, err := applyBloFinCopySyncPayload(state, blofinSyncTestConfig(), blofinSyncTestPayload(), nil, now)
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
	out2, err := applyBloFinCopySyncPayload(state, blofinSyncTestConfig(), blofinSyncTestPayload(), nil, now)
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
	if got, _, handled := bookBloFinCopyResidual(db, 0.05, time.Now().UTC(), "window-small"); got != 0 || handled {
		t.Fatalf("sub-threshold residual booked %v", got)
	}
	if total := blofinCopyAdjustmentsTotal(db); total != 0 {
		t.Fatalf("adjustments total=%v, want 0", total)
	}
	got, detail, handled := bookBloFinCopyResidual(db, 0.3, time.Now().UTC(), "window-1")
	if diff := got - 0.3; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("residual=%v, want 0.3 (%s)", got, detail)
	}
	if !handled {
		t.Fatalf("residual should be handled")
	}
	if total := blofinCopyAdjustmentsTotal(db); total-0.3 > 1e-9 || total-0.3 < -1e-9 {
		t.Fatalf("adjustments total=%v, want 0.3", total)
	}
	// Same window twice must not double-book (idempotent dedup).
	if got, _, handled := bookBloFinCopyResidual(db, 0.3, time.Now().UTC(), "window-1"); got != 0 || !handled {
		t.Fatalf("second booking=%v, want 0 (dedup)", got)
	}
}

func TestSyncStateRoundTrip(t *testing.T) {
	db := blofinSyncTestDB(t)
	st, err := loadBloFinCopySyncState(db)
	if err != nil || st.Found {
		t.Fatalf("fresh state found=%v err=%v", st.Found, err)
	}
	want := blofinCopySyncState{FillsSinceMs: 1790558257392, TransfersSinceMs: 1790570000000, LastRunMs: 1790570000000, LastEquity: 351.27, LastLedger: 77.52, LastTradesNet: 77.52, LastAdjustments: -3.24, LastAccountNet: 74.28, PendingResidual: 0.04, Found: true}
	if err := storeBloFinCopySyncState(db, want); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err := loadBloFinCopySyncState(db)
	if err != nil || !got.Found {
		t.Fatalf("reload found=%v err=%v", got.Found, err)
	}
	if got.LastAccountNet-74.28 > 1e-9 || got.FillsSinceMs != want.FillsSinceMs || got.TransfersSinceMs != want.TransfersSinceMs || got.PendingResidual != want.PendingResidual {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestBloFinCopySyncDue(t *testing.T) {
	now := time.Now().UTC()
	if !blofinCopySyncDue(0, now) {
		t.Fatal("first run should be due")
	}
	if !blofinCopySyncDue(now.Add(-10*time.Minute).UnixMilli(), now) {
		t.Fatal("stale run should be due")
	}
	if blofinCopySyncDue(now.Add(-1*time.Minute).UnixMilli(), now) {
		t.Fatal("fresh run should not be due")
	}
}

func TestApplyBackfillsMissingCloseWithParentAlreadyTracked(t *testing.T) {
	const strategyID = "live-order_blocks-spcx-15m"
	openedAt := time.UnixMilli(1790587979013).UTC().Add(545 * time.Millisecond)
	state := NewAppState()
	state.Strategies[strategyID] = &StrategyState{
		ID: strategyID, Type: "perps", Platform: "blofin", Cash: 350, InitialCapital: 100,
		Positions: map[string]*Position{
			"SPCX": {Symbol: "SPCX", TradePositionID: "spcx-parent", Quantity: 503, InitialQuantity: 503, AvgCost: 149.02, Side: "long", Multiplier: 0.01, Leverage: 75, OpenedAt: openedAt},
		},
		TradeHistory: []Trade{{Timestamp: openedAt, StrategyID: strategyID, Symbol: "SPCX", PositionID: "spcx-parent", Side: "buy", Quantity: 503, Price: 149.02, TradeType: "perps", ExchangeOrderID: "17008358", ExchangeFee: 0.44974236}},
	}
	cfg := []StrategyConfig{{ID: strategyID, Type: "perps", Platform: "blofin", Args: []string{"order_blocks", "SPCX", "15m", "--mode=live"}, Direction: DirectionLong, Leverage: 75}}
	payload := &blofinCopySyncPayload{
		Incremental: true,
		Positions: []blofinCopySyncPosition{{
			OrderID: "17008358", InstID: "SPCX-USDT", Symbol: "SPCX", Side: "buy", PositionSide: "long",
			Quantity: "503", OpenPrice: "149.02", OpenMs: 1790587979013, CloseMs: 1790588285449,
			ClosePrice: "149.31", RealizedPnL: "1.4587", CloseType: "close", ContractValue: "0.01",
			Closes: []blofinCopySyncClose{{CloseOrderID: "7371387", Side: "sell", Size: "503", AveragePrice: "149.31", Fee: "0.45061758", RealizedPnl: "1.45", OrderTime: 1790588285449}},
		}},
		Orders: map[string]blofinCopySyncOrder{
			"17008358": {OrderID: "17008358", InstID: "SPCX-USDT", Side: "buy", FilledSize: "503", AveragePrice: "149.02", Fee: "0.44974236", CreateTime: 1790587979013},
			"17008467": {OrderID: "17008467", InstID: "SPCX-USDT", Side: "sell", FilledSize: "503", AveragePrice: "149.31", Fee: "0.45061758", Pnl: "1.4587", CreateTime: 1790588285449},
		},
	}
	db := blofinSyncTestDB(t)
	if err := db.SaveState(state); err != nil {
		t.Fatalf("save source: %v", err)
	}
	store := singleFileStore(db)
	now := time.UnixMilli(1790589000000).UTC()
	out, err := applyBloFinCopySyncPayload(state, cfg, payload, store, now)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out.PositionsInserted != 1 || out.TradesInserted != 1 || len(state.Strategies[strategyID].Positions) != 0 {
		t.Fatalf("outcome=%+v positions=%+v", out, state.Strategies[strategyID].Positions)
	}
	if len(out.RecoveredCloseTrades) != 1 || out.RecoveredCloseTrades[0].Trade.ExchangeOrderID != "17008467" {
		t.Fatalf("recovered close alerts=%+v, want close OID 17008467", out.RecoveredCloseTrades)
	}
	if math.Abs(out.LedgerDelta-(1.4587-0.45061758)) > 1e-9 {
		t.Fatalf("ledger delta=%v, want %.8f", out.LedgerDelta, 1.4587-0.45061758)
	}
	if err := db.SaveState(state); err != nil {
		t.Fatalf("save repaired: %v", err)
	}
	var count int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM trades WHERE exchange_order_id=? AND is_close=1", "17008467").Scan(&count); err != nil || count != 1 {
		t.Fatalf("close order rows=%d err=%v", count, err)
	}
	// A retry must see the close OID and avoid double-booking.
	out2, err := applyBloFinCopySyncPayload(state, cfg, payload, store, now.Add(5*time.Minute))
	if err != nil || out2.TradesInserted != 0 {
		t.Fatalf("retry outcome=%+v err=%v", out2, err)
	}
}

func TestApplyBloFinCopyCloseFillsSummarizeFullPosition(t *testing.T) {
	const strategyID = "live-order_blocks-spcx-15m"
	openedAt := time.UnixMilli(1790601906134).UTC()
	state := &StrategyState{
		ID: strategyID, Type: "perps", Platform: "blofin", Cash: 100,
		Positions: map[string]*Position{
			"SPCX": {
				Symbol: "SPCX", TradePositionID: "spcx-parent", Quantity: 500,
				InitialQuantity: 500, AvgCost: 149.96996, Side: "long",
				Multiplier: 0.01, OpenedAt: openedAt,
			},
		},
		TradeHistory: []Trade{{
			StrategyID: strategyID, Symbol: "SPCX", PositionID: "spcx-parent",
			Side: "buy", Quantity: 500, Price: 149.96996, ExchangeOrderID: "17014709",
		}},
	}
	openMs := openedAt.UnixMilli()

	firstNet, err := applyBloFinCopyCloseFill(state, "SPCX", "long", openMs, blofinCopySyncClose{
		CloseOrderID: "7372889", Side: "sell", Size: "250", AveragePrice: "150.75",
		Fee: "0.226125", RealizedPnl: "1.95", OrderTime: openMs + 600_000,
	}, blofinCopySyncOrder{OrderID: "17014884", Side: "sell"}, 0.01)
	if err != nil {
		t.Fatalf("apply first partial close: %v", err)
	}
	if math.Abs(firstNet-(1.95-0.226125)) > 1e-9 {
		t.Fatalf("first close net=%v", firstNet)
	}
	if got := state.Positions["SPCX"].Quantity; got != 250 {
		t.Fatalf("remaining quantity=%v, want 250", got)
	}
	if len(state.ClosedPositions) != 0 {
		t.Fatalf("closed positions after partial fill=%d, want 0", len(state.ClosedPositions))
	}
	if !strings.Contains(state.TradeHistory[1].Details, "partial-close") {
		t.Fatalf("partial close details=%q", state.TradeHistory[1].Details)
	}

	secondNet, err := applyBloFinCopyCloseFill(state, "SPCX", "long", openMs, blofinCopySyncClose{
		CloseOrderID: "7372940", Side: "sell", Size: "250", AveragePrice: "150.25",
		Fee: "0.225375", RealizedPnl: "0.7002", OrderTime: openMs + 1_500_000,
	}, blofinCopySyncOrder{OrderID: "17015301", Side: "sell"}, 0.01)
	if err != nil {
		t.Fatalf("apply final close: %v", err)
	}
	if math.Abs(secondNet-(0.7002-0.225375)) > 1e-9 {
		t.Fatalf("second close net=%v", secondNet)
	}
	if _, ok := state.Positions["SPCX"]; ok {
		t.Fatal("position remained open after final close")
	}
	if len(state.ClosedPositions) != 1 {
		t.Fatalf("closed positions=%d, want 1", len(state.ClosedPositions))
	}
	closed := state.ClosedPositions[0]
	if closed.Quantity != 500 {
		t.Errorf("closed quantity=%v, want 500", closed.Quantity)
	}
	if math.Abs(closed.ClosePrice-150.50) > 1e-9 {
		t.Errorf("closed VWAP=%v, want 150.50", closed.ClosePrice)
	}
	if math.Abs(closed.RealizedPnL-2.1987) > 1e-9 {
		t.Errorf("closed net PnL=%v, want 2.1987", closed.RealizedPnL)
	}
	if strings.Contains(state.TradeHistory[2].Details, "partial-close") {
		t.Errorf("final close details unexpectedly say partial: %q", state.TradeHistory[2].Details)
	}
}

func TestNotifyBloFinCopyRecoveredCloseTradesUsesTradeAlerts(t *testing.T) {
	const strategyID = "live-order_blocks-spcx-15m"
	sc := StrategyConfig{
		ID: strategyID, Type: "perps", Platform: "blofin",
		Args: []string{"order_blocks", "SPCX", "15m", "--mode=live"},
	}
	mock := &mockNotifier{}
	notifier := &MultiNotifier{backends: []notifierBackend{{
		notifier: mock, tradeAlertChannels: map[string]string{"default": "blofin-trades"},
	}}}
	trades := []blofinCopyRecoveredClose{
		{StrategyID: strategyID, Trade: Trade{
			StrategyID: strategyID, Symbol: "SPCX", Side: "sell", Quantity: 250, Price: 150.75,
			Value: 376.875, ExchangeOrderID: "17014884", IsClose: true,
			Details: "BloFin incremental Copy partial-close fill recovery",
		}},
		{StrategyID: strategyID, Trade: Trade{
			StrategyID: strategyID, Symbol: "SPCX", Side: "sell", Quantity: 250, Price: 150.25,
			Value: 375.625, ExchangeOrderID: "17015301", IsClose: true,
			Details: "BloFin incremental Copy close fill recovery",
		}},
	}

	notifyBloFinCopyRecoveredCloseTrades(notifier, []StrategyConfig{sc}, nil, trades)

	if len(mock.messages) != 2 {
		t.Fatalf("trade-alert messages=%d, want 2: %+v", len(mock.messages), mock.messages)
	}
	if !strings.Contains(mock.messages[0].content, "TRADE PARTIAL") || !strings.Contains(mock.messages[0].content, "17014884") {
		t.Errorf("partial close alert=%q", mock.messages[0].content)
	}
	if !strings.Contains(mock.messages[1].content, "TRADE CLOSED") || !strings.Contains(mock.messages[1].content, "17015301") {
		t.Errorf("final close alert=%q", mock.messages[1].content)
	}
}

func TestApplyDoesNotTreatNewSameSymbolPositionAsStaleParent(t *testing.T) {
	const strategyID = "live-order_blocks-spcx-15m"
	parentOpenMs, parentCloseMs := int64(1790587979013), int64(1790588285449)
	newOpenAt := time.UnixMilli(parentCloseMs + 10_000).UTC()
	state := NewAppState()
	state.Strategies[strategyID] = &StrategyState{
		ID: strategyID, Type: "perps", Platform: "blofin", Cash: 350, InitialCapital: 100,
		Positions: map[string]*Position{
			"SPCX": {Symbol: "SPCX", TradePositionID: "new-parent", Quantity: 500, InitialQuantity: 500, AvgCost: 149.97, Side: "long", Multiplier: 0.01, Leverage: 75, OpenedAt: newOpenAt},
		},
		TradeHistory: []Trade{
			{Timestamp: time.UnixMilli(parentOpenMs).UTC(), StrategyID: strategyID, Symbol: "SPCX", Side: "buy", Quantity: 503, Price: 149.02, ExchangeOrderID: "17008358"},
			{Timestamp: time.UnixMilli(parentCloseMs).UTC(), StrategyID: strategyID, Symbol: "SPCX", Side: "sell", Quantity: 503, Price: 149.31, ExchangeOrderID: "17008467", IsClose: true, RealizedPnL: 1.4587, ExchangeFee: 0.45061758, PnLGross: true},
		},
	}
	cfg := []StrategyConfig{{ID: strategyID, Type: "perps", Platform: "blofin", Args: []string{"order_blocks", "SPCX", "15m", "--mode=live"}, Direction: DirectionLong, Leverage: 75}}
	payload := &blofinCopySyncPayload{
		Incremental: true,
		Positions: []blofinCopySyncPosition{{
			OrderID: "17008358", InstID: "SPCX-USDT", Symbol: "SPCX", Side: "buy", PositionSide: "long",
			Quantity: "503", OpenPrice: "149.02", OpenMs: parentOpenMs, CloseMs: parentCloseMs,
			ClosePrice: "149.31", RealizedPnL: "1.4587", CloseType: "close", ContractValue: "0.01",
			Closes: []blofinCopySyncClose{{CloseOrderID: "7371387", Side: "sell", Size: "503", AveragePrice: "149.31", Fee: "0.45061758", RealizedPnl: "1.45", OrderTime: parentCloseMs}},
		}},
		Orders: map[string]blofinCopySyncOrder{
			"17008358": {OrderID: "17008358", InstID: "SPCX-USDT", Side: "buy", FilledSize: "503", AveragePrice: "149.02", Fee: "0.44974236", CreateTime: parentOpenMs},
			"17008467": {OrderID: "17008467", InstID: "SPCX-USDT", Side: "sell", FilledSize: "503", AveragePrice: "149.31", Fee: "0.45061758", Pnl: "1.4587", CreateTime: parentCloseMs},
		},
	}
	out, err := applyBloFinCopySyncPayload(state, cfg, payload, nil, newOpenAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out.Skipped) != 0 || out.TradesInserted != 0 || out.PositionsInserted != 0 {
		t.Fatalf("old parent sync outcome=%+v, should be a clean no-op", out)
	}
	if p := state.Strategies[strategyID].Positions["SPCX"]; p == nil || p.Quantity != 500 || !p.OpenedAt.Equal(newOpenAt) {
		t.Fatalf("new same-symbol position was changed: %+v", p)
	}
}

func TestRealizedNetPnLForStrategy(t *testing.T) {
	db := blofinSyncTestDB(t)
	if _, err := db.db.Exec(`INSERT INTO trades (strategy_id,timestamp,symbol,side,quantity,price,value,is_close,realized_pnl,exchange_fee,pnl_gross,fee_source) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		"s1", "2026-09-25T00:00:00Z", "DOGE", "buy", 1, 1, 1, 0, 0.0, 0.3, 1, "userfills"); err != nil {
		t.Fatalf("insert open: %v", err)
	}
	if _, err := db.db.Exec(`INSERT INTO trades (strategy_id,timestamp,symbol,side,quantity,price,value,is_close,realized_pnl,exchange_fee,pnl_gross,fee_source) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		"s1", "2026-09-25T00:00:00Z", "DOGE", "sell", 1, 1, 1, 1, 10.0, 0.5, 1, "userfills"); err != nil {
		t.Fatalf("insert close: %v", err)
	}
	got, err := db.RealizedNetPnLForStrategy("s1")
	if err != nil {
		t.Fatalf("net: %v", err)
	}
	// Full ledger: open -0.3 + close (10.0-0.5) = 9.2.
	if got-9.2 > 1e-9 || got-9.2 < -1e-9 {
		t.Fatalf("net=%v, want 9.2", got)
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
