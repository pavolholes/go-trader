package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCompletedBarsHeldCountsOnlyElapsedCandleBoundaries(t *testing.T) {
	opened := time.Date(2026, 10, 3, 10, 7, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		want int
	}{
		{"same unfinished candle", time.Date(2026, 10, 3, 10, 14, 59, 0, time.UTC), 0},
		{"one completed candle", time.Date(2026, 10, 3, 10, 15, 0, 0, time.UTC), 1},
		{"two completed candles after restart", time.Date(2026, 10, 3, 10, 30, 0, 0, time.UTC), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := completedBarsHeld(opened, tt.now, "15m")
			if !ok || got != tt.want {
				t.Fatalf("completedBarsHeld() = %d, %v; want %d, true", got, ok, tt.want)
			}
		})
	}
	if got, ok := completedBarsHeld(time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), "1M"); !ok || got != 1 {
		t.Fatalf("monthly completed bars=%d, valid=%v; want 1, true", got, ok)
	}
}

func TestBloFinSupportMatrixDeclaresContextAndRiskOwnerForEveryRegisteredClose(t *testing.T) {
	matrix, err := readCloseSupportMatrix()
	if err != nil {
		t.Fatal(err)
	}
	entries := matrix.Platforms["blofin-perps"]
	if len(entries) != 12 {
		t.Fatalf("BloFin support entries=%d, want 12 registered evaluators", len(entries))
	}
	for name, support := range entries {
		if !support.Supported || len(support.RequiredContext) == 0 || support.StopOwner == "" || support.Notes == "" {
			t.Errorf("incomplete support row %q: %+v", name, support)
		}
	}
}

func TestBloFinSupportRequiresAnIndependentStopForTPOnlyCloses(t *testing.T) {
	base := StrategyConfig{
		ID:       "bl-time",
		Type:     "perps",
		Platform: "blofin",
		Args:     []string{"momentum", "BTC", "15m", "--mode=live"},
		CloseStrategy: &StrategyRef{
			Name:   "time_stop",
			Params: map[string]interface{}{"max_bars": 12},
		},
	}
	if errs := validateBloFinCloseSupport(&Config{Strategies: []StrategyConfig{base}}); len(errs) == 0 || !strings.Contains(strings.Join(errs, " "), "independent software-managed stop owner") {
		t.Fatalf("missing ATR stop validation errors=%v", errs)
	}
	mult := 1.75
	base.StopLossATRMult = &mult
	if errs := validateBloFinCloseSupport(&Config{Strategies: []StrategyConfig{base}}); len(errs) != 0 {
		t.Fatalf("configured independent stop should validate, got %v", errs)
	}
}

func TestBloFinContextCloseParamsCannotBeConfiguredToNoOp(t *testing.T) {
	stop := 1.5
	for _, tc := range []struct {
		name   string
		params map[string]interface{}
		want   string
	}{
		{"time_stop", map[string]interface{}{"max_bars": 0}, "max_bars"},
		{"zscore_target", map[string]interface{}{"lookback": 1, "z_target": 1.5}, "lookback"},
		{"zscore_target", map[string]interface{}{"lookback": 20, "z_target": 0}, "z_target"},
		{"atr_stop", map[string]interface{}{"atr_mult": 1.5, "atr_source": "bad"}, "atr_source"},
	} {
		t.Run(tc.name+"/"+tc.want, func(t *testing.T) {
			sc := StrategyConfig{
				ID: "bl-close", Type: "perps", Platform: "blofin",
				Args:            []string{"momentum", "BTC", "15m", "--mode=live"},
				StopLossATRMult: &stop,
				CloseStrategy:   &StrategyRef{Name: tc.name, Params: tc.params},
			}
			errs := validateBloFinCloseSupport(&Config{Strategies: []StrategyConfig{sc}})
			if !strings.Contains(strings.Join(errs, " "), tc.want) {
				t.Fatalf("validation errors=%v, want %q", errs, tc.want)
			}
		})
	}
}

func TestBloFinRatchetConfigIsAcceptedOnlyWithItsVirtualTrailOwner(t *testing.T) {
	initial := 2.0
	sc := StrategyConfig{
		ID: "bl-ratchet-config", Type: "perps", Platform: "blofin",
		TrailingStopATRMult: &initial,
		CloseStrategy: &StrategyRef{Name: "trailing_tp_ratchet", Params: map[string]interface{}{
			"tp_tiers": []interface{}{map[string]interface{}{
				"atr_multiple": 1.0, "trailing_mult_after": 1.0,
			}},
		}},
	}
	if errs := validateTrailingTPRatchetClose(sc, canonicalTrendRegimeLabels, false); len(errs) != 0 {
		t.Fatalf("BloFin perps ratchet should validate with its ATR trail owner: %v", errs)
	}
	sc.TrailingStopATRMult = nil
	if errs := validateTrailingTPRatchetClose(sc, canonicalTrendRegimeLabels, false); len(errs) == 0 {
		t.Fatal("ratchet without a stop owner must fail configuration validation")
	}
}

func TestBloFinVirtualRatchetStopPersistsAndClosesLongAndShort(t *testing.T) {
	for _, tc := range []struct {
		side     string
		mark     float64
		wantSig  int
		wantStop float64
		wantHigh float64
	}{
		{side: "long", mark: 111, wantSig: -1, wantStop: 106, wantHigh: 111},
		{side: "short", mark: 89, wantSig: 1, wantStop: 94, wantHigh: 89},
	} {
		t.Run(tc.side, func(t *testing.T) {
			mult := 1.0
			sc := StrategyConfig{
				ID: "bl-ratchet", Platform: "blofin", Type: "perps",
				Args:                []string{"momentum", "ETH", "1h", "--mode=paper"},
				TrailingStopATRMult: &mult,
				CloseStrategy: &StrategyRef{Name: "trailing_tp_ratchet", Params: map[string]interface{}{
					"tp_tiers": []interface{}{map[string]interface{}{
						"atr_multiple": 1.0, "trailing_mult_after": 0.5,
					}},
				}},
			}
			pos := &Position{Symbol: "ETH", Side: tc.side, Quantity: 1, InitialQuantity: 1, AvgCost: 100, RiskAnchorPrice: 100, EntryATR: 10, OpenedAt: time.Now().UTC()}
			state := &StrategyState{Positions: map[string]*Position{"ETH": pos}}
			armBloFinVirtualStopOnOpen(sc, pos)
			if pos.StopLossTriggerPx == 0 {
				t.Fatal("open did not arm the initial software stop")
			}
			var mu sync.RWMutex
			result, alert := prepareBloFinVirtualClose(sc, state, "ETH", tc.mark, &mu, nil)
			if alert == nil {
				t.Fatal("cleared ratchet tier should produce a tightening event")
			}
			if result != nil {
				t.Fatalf("favorable mark unexpectedly stopped out: %+v", result)
			}
			if pos.StopLossHighWaterPx != tc.wantHigh || pos.StopLossTriggerPx != tc.wantStop {
				t.Fatalf("persisted ratchet state high_water=%.2f trigger=%.2f, want %.2f/%.2f", pos.StopLossHighWaterPx, pos.StopLossTriggerPx, tc.wantHigh, tc.wantStop)
			}
			breach := tc.wantStop - 0.1
			if tc.side == "short" {
				breach = tc.wantStop + 0.1
			}
			result, _ = prepareBloFinVirtualClose(sc, state, "ETH", breach, &mu, nil)
			if result == nil || result.CloseFraction != 1 || result.Signal != tc.wantSig {
				t.Fatalf("breached virtual stop result=%+v, want full close signal %d", result, tc.wantSig)
			}
		})
	}
}

func TestBloFinDynamicRegimeStopHoldsPendingLabelAndNeverLoosens(t *testing.T) {
	stopAndTiers := func(stop float64) map[string]interface{} {
		return map[string]interface{}{
			"stop_loss_atr": stop,
			"tp_tiers":      []interface{}{map[string]interface{}{"atr_multiple": 1.0, "close_fraction": 1.0}},
		}
	}
	sc := StrategyConfig{
		ID: "bl-dynamic", Platform: "blofin", Type: "perps",
		Args: []string{"momentum", "ETH", "1h", "--mode=paper"},
		CloseStrategy: &StrategyRef{Name: dynamicCloseStrategyName, Params: map[string]interface{}{
			"regime_confirm_cycles": 2,
			"trend_regime": map[string]interface{}{
				"trending_up":   stopAndTiers(2.0),
				"trending_down": stopAndTiers(2.0),
				"ranging":       stopAndTiers(3.0),
			},
		}},
	}
	pos := &Position{
		Symbol: "ETH", Side: "long", Quantity: 1, InitialQuantity: 1,
		AvgCost: 100, RiskAnchorPrice: 100, EntryATR: 10,
		Regime: "trending_up", RegimeAppliedLabel: "trending_up",
		StopLossTriggerPx: 80,
	}
	state := &StrategyState{
		Regime:    "ranging",
		Positions: map[string]*Position{"ETH": pos},
	}
	var mu sync.RWMutex
	if got := advanceBloFinDynamicCloseRegime(sc, state, "ETH", 85, &mu, nil); got != nil {
		t.Fatalf("first pending cycle unexpectedly closed: %+v", got)
	}
	if pos.RegimeAppliedLabel != "trending_up" || pos.RegimePendingCount != 1 || pos.StopLossTriggerPx != 80 {
		t.Fatalf("pending state applied=%q count=%d stop=%.2f; want old label/count=1/80", pos.RegimeAppliedLabel, pos.RegimePendingCount, pos.StopLossTriggerPx)
	}
	if result, _ := prepareBloFinVirtualClose(sc, state, "ETH", 79, &mu, nil); result == nil || result.CloseFraction != 1 {
		t.Fatalf("adverse move through the old stop during confirmation must close; got %+v", result)
	}

	pos.StopLossTriggerPx = 80
	if got := advanceBloFinDynamicCloseRegime(sc, state, "ETH", 85, &mu, nil); got != nil {
		t.Fatalf("second confirmation cycle unexpectedly closed: %+v", got)
	}
	if pos.RegimeAppliedLabel != "ranging" {
		t.Fatalf("applied regime=%q, want confirmed ranging", pos.RegimeAppliedLabel)
	}
	if pos.StopLossTriggerPx != 80 {
		t.Fatalf("looser new regime stop moved the trigger to %.2f; want old tighter stop 80", pos.StopLossTriggerPx)
	}
}

func TestBloFinActiveRatchetParametersCannotHotReloadAcrossOpenPosition(t *testing.T) {
	initial := 2.0
	old := StrategyConfig{
		ID: "bl-ratchet", Platform: "blofin", Type: "perps",
		TrailingStopATRMult: &initial,
		CloseStrategy: &StrategyRef{Name: "trailing_tp_ratchet", Params: map[string]interface{}{
			"tp_tiers": []interface{}{map[string]interface{}{
				"atr_multiple": 1.0, "trailing_mult_after": 1.0,
			}},
		}},
	}
	next := old
	next.CloseStrategy = cloneCloseStrategyRef(old.CloseStrategy)
	next.CloseStrategy.Params = map[string]interface{}{
		"tp_tiers": []interface{}{map[string]interface{}{
			"atr_multiple": 1.0, "trailing_mult_after": 0.5,
		}},
	}
	cfg := &Config{Strategies: []StrategyConfig{old}}
	nextConfig := &Config{Strategies: []StrategyConfig{next}}
	state := &AppState{Strategies: map[string]*StrategyState{
		old.ID: {Positions: map[string]*Position{
			"ETH": {Symbol: "ETH", Side: "long", Quantity: 1, AvgCost: 100, EntryATR: 5},
		}},
	}}
	if err := validateHotReloadStateCompatible(cfg, nextConfig, state); err == nil || !strings.Contains(err.Error(), "trailing_tp_ratchet tier table changed") {
		t.Fatalf("active BloFin ratchet config reload error=%v, want ratchet tier-table hold", err)
	}
}
