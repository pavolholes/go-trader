package main

import (
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBloFinPendingFillAlertIsNotScriptFailure(t *testing.T) {
	sc := StrategyConfig{ID: "live-order_blocks-spcx-15m", Platform: "blofin", Type: "perps"}
	mock := &mockNotifier{}
	notifier := &MultiNotifier{backends: []notifierBackend{{
		notifier: mock, tradeAlertChannels: map[string]string{"default": "blofin-trades"},
	}}}

	notifyBloFinOrderFillPending(notifier, sc, "SELL", "SPCX", "17014884", "client-abc")

	if len(mock.messages) != 1 {
		t.Fatalf("pending-fill messages=%d, want 1", len(mock.messages))
	}
	message := mock.messages[0].content
	if strings.Contains(message, "SIGNAL SCRIPT FAILING") {
		t.Fatalf("pending venue fill was mislabeled as script failure: %q", message)
	}
	for _, want := range []string{"FILL CONFIRMATION PENDING", sc.ID, "SELL SPCX", "17014884", "client-abc", "order was rejected"} {
		if !strings.Contains(message, want) {
			t.Errorf("pending-fill message %q is missing %q", message, want)
		}
	}
}

func TestRunBloFinCheckPassesNoEdgeAcknowledgement(t *testing.T) {
	old := runBloFinCheckFn
	t.Cleanup(func() { runBloFinCheckFn = old })
	var gotArgs []string
	runBloFinCheckFn = func(_ string, args []string) (*BloFinResult, string, error) {
		gotArgs = append([]string(nil), args...)
		return &BloFinResult{Symbol: "BTC", Price: 100, Mode: "live"}, "", nil
	}
	acknowledged := true
	sc := StrategyConfig{
		ID: "live-no-edge-btc", Platform: "blofin", Type: "perps",
		Script:      "shared_scripts/check_blofin.py",
		Args:        []string{"sma_crossover", "BTC", "1h", "--mode=live"},
		AllowNoEdge: &acknowledged,
	}
	logger := &StrategyLogger{stratID: sc.ID, writer: io.Discard}
	_, _, _, ok := runBloFinCheck(sc, nil, PositionCtx{}, nil, nil, logger)
	if !ok {
		t.Fatal("mock BloFin check failed")
	}
	if !strings.Contains(strings.Join(gotArgs, " "), allowNoEdgeFlag) {
		t.Fatalf("BloFin check argv %v does not contain %s", gotArgs, allowNoEdgeFlag)
	}
}

func TestAppendBloFinPositionContextArgsCarriesPersistedCloseInputs(t *testing.T) {
	openedAt := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	got := appendBloFinPositionContextArgs([]string{"momentum", "BTC", "15m"}, PositionCtx{
		Quantity:           1,
		OpenedAt:           openedAt,
		BarsHeld:           8,
		BarsHeldKnown:      true,
		RegimeAppliedLabel: "ranging",
		RegimePendingLabel: "trending_up",
		RegimePendingCount: 1,
	})
	want := []string{
		"momentum", "BTC", "15m",
		"--position-opened-at-ms", strconv.FormatInt(openedAt.UnixMilli(), 10),
		"--position-bars-held", "8",
		"--position-regime-applied", "ranging",
		"--position-regime-pending-label", "trending_up",
		"--position-regime-pending-count", "1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BloFin position args=%v, want %v", got, want)
	}
}

func TestBloFinManageOnlyStillExecutesRiskReducingClose(t *testing.T) {
	for _, tc := range []struct {
		side string
		want int
	}{{"long", -1}, {"short", 1}} {
		result := &BloFinResult{StrategyDecisionFields: StrategyDecisionFields{CloseFraction: 0.5}, Signal: 0}
		applyBloFinManageOnly(result, 2, tc.side)
		if result.Signal != tc.want || result.CloseFraction != 0.5 {
			t.Errorf("manage-only %s result=%+v, want close signal %d", tc.side, result, tc.want)
		}
	}
	entry := &BloFinResult{Signal: 1}
	applyBloFinManageOnly(entry, 0, "")
	if entry.Signal != 0 {
		t.Fatalf("manage-only must suppress flat entry, signal=%d", entry.Signal)
	}
}

func TestExecuteBloFinResultBooksEvaluatorCloseFractionAndAttribution(t *testing.T) {
	state := &StrategyState{
		ID: "bl-btc", Platform: "blofin", Type: "perps", Cash: 1000,
		Positions: map[string]*Position{
			"BTC": {
				Symbol: "BTC", Side: "long", Quantity: 4, InitialQuantity: 4,
				AvgCost: 100, EntryATR: 10, RiskAnchorPrice: 100,
				OpenedAt: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
			},
		},
	}
	closeResult := &BloFinResult{
		StrategyDecisionFields: StrategyDecisionFields{
			CloseFraction:  0.25,
			CloseStrategy:  "tiered_tp_atr_regime",
			CloseEvaluator: "tiered_tp_atr_regime",
			CloseSource:    "evaluator",
			CloseReason:    "tiered_tp_atr_regime:trending_up:1.5",
			TPTier:         1.5,
			StopLossPrice:  90,
		},
		Symbol: "BTC", Signal: -1, Price: 105,
	}
	logger := &StrategyLogger{stratID: state.ID, writer: io.Discard}
	sc := StrategyConfig{
		ID: state.ID, Platform: "blofin", Type: "perps",
		Args:          []string{"momentum", "BTC", "1h", "--mode=paper"},
		CloseStrategy: &StrategyRef{Name: "tiered_tp_atr_regime"},
	}
	if _, _ = executeBloFinResult(sc, state, nil, closeResult, nil, "SELL", 105, nil, logger, nil); state.Positions["BTC"].Quantity != 3 {
		t.Fatalf("remaining quantity=%g, want 3 after 25%% close", state.Positions["BTC"].Quantity)
	}
	if len(state.TradeHistory) != 1 {
		t.Fatalf("close trade rows=%d, want 1", len(state.TradeHistory))
	}
	trade := state.TradeHistory[0]
	if trade.Quantity != 1 {
		t.Fatalf("closed quantity=%g, want 1 of 4", trade.Quantity)
	}
	for _, want := range []string{
		"close_source=evaluator",
		"close_evaluator=tiered_tp_atr_regime",
		"close_reason=tiered_tp_atr_regime:trending_up:1.5",
		"tp_tier=1.5",
		"sl_trigger_px=90",
	} {
		if !strings.Contains(trade.Details, want) {
			t.Errorf("close details %q missing %q", trade.Details, want)
		}
	}
}
