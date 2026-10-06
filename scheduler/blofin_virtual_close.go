package main

import (
	"fmt"
	"strings"
	"sync"
)

func blofinCloseNeedsEntryATR(sc StrategyConfig) bool {
	if strategyUsesTrailingTPRatchetClose(sc) || strategyUsesDynamicRegimeClose(sc) || blofinHasStrategyATRStop(sc) {
		return true
	}
	for _, ref := range sc.closeRefs() {
		name := strings.ToLower(strings.TrimSpace(ref.Name))
		switch name {
		case "tiered_tp_atr", "tiered_tp_atr_live", "tiered_tp_atr_regime", "tiered_tp_atr_live_regime":
			return true
		case "atr_stop":
			if strings.ToLower(strings.TrimSpace(fmt.Sprint(firstPresent(ref.Params, "atr_source")))) != "live" {
				return true
			}
		case "avwap_stop":
			if strings.ToLower(strings.TrimSpace(fmt.Sprint(firstPresent(ref.Params, "atr_source")))) == "entry" {
				return true
			}
		}
	}
	return false
}

func blofinVirtualStopATR(sc StrategyConfig, pos *Position) (mult, anchor float64, owner, reason string, ok bool) {
	if pos == nil || pos.EntryATR <= 0 || pos.AvgCost <= 0 {
		return 0, 0, "", "", false
	}
	label := protectionATRRegimeLabel(pos, sc)
	if strategyUsesUnifiedRegimeClose(sc) {
		if v, found := unifiedCloseStopLossATR(sc, label); found {
			return v, pos.riskAnchorPrice(), dynamicCloseStrategyName,
				fmt.Sprintf("unified_regime_stop:%s:%g", label, v), true
		}
		return 0, 0, "", "", false
	}
	if sc.StopLossATRMultRegime != nil && !sc.StopLossATRMultRegime.IsZero() {
		if label == "" {
			return 0, 0, "", "", false
		}
		if v, found := resolveRegimeATR(*sc.StopLossATRMultRegime, label); found {
			return v, pos.riskAnchorPrice(), "stop_loss_atr_mult_regime",
				fmt.Sprintf("stop_loss_atr_mult_regime:%s:%g", label, v), true
		}
		return 0, 0, "", "", false
	}
	if sc.StopLossATRMult != nil && *sc.StopLossATRMult > 0 {
		return *sc.StopLossATRMult, pos.riskAnchorPrice(), "stop_loss_atr_mult",
			fmt.Sprintf("stop_loss_atr_mult:%g", *sc.StopLossATRMult), true
	}
	for _, ref := range sc.closeRefs() {
		name := strings.ToLower(strings.TrimSpace(ref.Name))
		if name != "tiered_tp_atr" && name != "tiered_tp_atr_regime" {
			continue
		}
		v, err := floatFromAnyChecked(ref.Params["sl_atr_mult"])
		if err == nil && v > 0 {
			return v, pos.AvgCost, name, fmt.Sprintf("sl_hit:%g", v), true
		}
	}
	return 0, 0, "", "", false
}

func blofinATRTrailTrigger(side string, highWater, entryATR, mult float64) float64 {
	if highWater <= 0 || entryATR <= 0 || mult <= 0 {
		return 0
	}
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "long":
		return highWater - entryATR*mult
	case "short":
		return highWater + entryATR*mult
	default:
		return 0
	}
}

func blofinFixedATRTrigger(side string, anchor, entryATR, mult float64) float64 {
	if anchor <= 0 || entryATR <= 0 || mult <= 0 {
		return 0
	}
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "long":
		return anchor - entryATR*mult
	case "short":
		return anchor + entryATR*mult
	default:
		return 0
	}
}

func blofinMarkBreachesStop(side string, mark, trigger float64) bool {
	if mark <= 0 || trigger <= 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "long":
		return mark <= trigger
	case "short":
		return mark >= trigger
	default:
		return false
	}
}

func blofinRatchetStopState(sc StrategyConfig, pos *Position, mark float64, logger *StrategyLogger) (*RatchetTriggerAlert, float64) {
	if pos == nil || !strategyUsesTrailingTPRatchetClose(sc) || pos.EntryATR <= 0 {
		return nil, 0
	}
	anchor := pos.riskAnchorPrice()
	if anchor <= 0 {
		anchor = pos.AvgCost
	}
	if pos.StopLossHighWaterPx <= 0 {
		pos.StopLossHighWaterPx = anchor
	}
	switch strings.ToLower(strings.TrimSpace(pos.Side)) {
	case "long":
		if mark > pos.StopLossHighWaterPx {
			pos.StopLossHighWaterPx = mark
		}
	case "short":
		if pos.StopLossHighWaterPx <= 0 || mark < pos.StopLossHighWaterPx {
			pos.StopLossHighWaterPx = mark
		}
	default:
		return nil, 0
	}
	_, alert := applyTrailingTPRatchetToPosition(sc, pos, pos.Symbol, mark, logger)
	mult := effectiveTrailingRatchetMult(pos, sc)
	trigger := blofinATRTrailTrigger(pos.Side, pos.StopLossHighWaterPx, pos.EntryATR, mult)
	if trigger > 0 {
		pos.StopLossTriggerPx = trigger
	}
	return alert, trigger
}

func armBloFinVirtualStopOnOpen(sc StrategyConfig, pos *Position) {
	if sc.Platform != "blofin" || sc.Type != "perps" || pos == nil || pos.EntryATR <= 0 {
		return
	}
	if strategyUsesTrailingTPRatchetClose(sc) {
		anchor := pos.riskAnchorPrice()
		if anchor <= 0 {
			anchor = pos.AvgCost
		}
		pos.StopLossHighWaterPx = anchor
		mult := effectiveTrailingRatchetMult(pos, sc)
		if trigger := blofinATRTrailTrigger(pos.Side, anchor, pos.EntryATR, mult); trigger > 0 {
			pos.StopLossTriggerPx = trigger
		}
		return
	}
	if strategyUsesDynamicRegimeClose(sc) && pos.RegimeAppliedLabel == "" {
		pos.RegimeAppliedLabel = positionATRRegimeLabel(pos, sc)
	}
	if mult, anchor, _, _, ok := blofinVirtualStopATR(sc, pos); ok {
		if trigger := blofinFixedATRTrigger(pos.Side, anchor, pos.EntryATR, mult); trigger > 0 {
			pos.StopLossTriggerPx = trigger
		}
	}
}

func blofinVirtualStopResult(sc StrategyConfig, pos *Position, symbol string, mark, trigger float64, evaluator, reason string) *BloFinResult {
	if pos == nil || pos.Quantity <= 0 || !blofinMarkBreachesStop(pos.Side, mark, trigger) {
		return nil
	}
	signal := -1
	if strings.EqualFold(pos.Side, "short") {
		signal = 1
	}
	result := &BloFinResult{
		Strategy:  effectiveOpenStrategy(sc),
		Symbol:    symbol,
		Timeframe: strategyDisplayTimeframe(sc),
		Signal:    signal,
		Price:     mark,
		Mode:      "paper",
		Platform:  "blofin",
	}
	if blofinIsLive(sc.Args) {
		result.Mode = "live"
	}
	result.CloseFraction = 1
	result.StopLossPrice = trigger
	result.ATRValue = pos.EntryATR
	result.CloseStrategy = ""
	if sc.CloseStrategy != nil {
		result.CloseStrategy = sc.CloseStrategy.Name
	}
	if evaluator != "stop_loss_atr_mult" && evaluator != "stop_loss_atr_mult_regime" {
		result.CloseEvaluator = evaluator
	}
	switch strings.ToLower(strings.TrimSpace(evaluator)) {
	case "tiered_tp_atr", "tiered_tp_atr_regime", "tiered_tp_atr_live_regime", dynamicCloseStrategyName,
		trailingTPRatchetCloseName, trailingTPRatchetRegimeCloseName:
		result.CloseSource = "evaluator"
	default:
		result.CloseSource = "risk_stop"
	}
	result.CloseReason = reason
	return result
}

// prepareBloFinVirtualClose updates and checks position-owned virtual stop
// state before the Python signal check. It uses the independent mark feed, so a
// candle-fetch failure cannot suppress an already-armed ratchet or ATR stop.
func prepareBloFinVirtualClose(sc StrategyConfig, state *StrategyState, symbol string, mark float64, mu *sync.RWMutex, logger *StrategyLogger) (*BloFinResult, *RatchetTriggerAlert) {
	if sc.Platform != "blofin" || sc.Type != "perps" || state == nil || symbol == "" || mark <= 0 || mu == nil {
		return nil, nil
	}
	mu.Lock()
	defer mu.Unlock()
	pos := state.Positions[symbol]
	if pos == nil || pos.Quantity <= 0 {
		return nil, nil
	}
	if pos.EntryATR <= 0 && blofinCloseNeedsEntryATR(sc) {
		if logger != nil {
			logger.InfoOnChange("blofin-close-missing-entry-atr", symbol,
				"BloFin close/stop management for %s requires a valid entry ATR; no virtual ATR stop can be evaluated until EntryATR is available", symbol)
		}
	}
	var alert *RatchetTriggerAlert
	var trigger float64
	var evaluator, reason string
	if strategyUsesTrailingTPRatchetClose(sc) {
		alert, trigger = blofinRatchetStopState(sc, pos, mark, logger)
		for _, ref := range sc.closeRefs() {
			if isTrailingTPRatchetCloseName(ref.Name) {
				evaluator = ref.Name
				break
			}
		}
		reason = "trailing_tp_ratchet:virtual_stop_breached"
	} else {
		if strategyUsesDynamicRegimeClose(sc) && pos.RegimeAppliedLabel == "" {
			pos.RegimeAppliedLabel = dynamicCloseATRRegimeLabel(pos, sc)
		}
		if mult, anchor, owner, stopReason, ok := blofinVirtualStopATR(sc, pos); ok {
			candidate := blofinFixedATRTrigger(pos.Side, anchor, pos.EntryATR, mult)
			if candidate > 0 {
				current := pos.StopLossTriggerPx
				if current <= 0 || !strategyUsesDynamicRegimeClose(sc) || (pos.Side == "long" && candidate > current) || (pos.Side == "short" && candidate < current) {
					pos.StopLossTriggerPx = candidate
				}
				trigger = pos.StopLossTriggerPx
			}
			evaluator = owner
			reason = stopReason
		}
		if trigger <= 0 {
			trigger = pos.StopLossTriggerPx
		}
	}
	return blofinVirtualStopResult(sc, pos, symbol, mark, trigger, evaluator, reason), alert
}

// advanceBloFinDynamicCloseRegime confirms a new label only after the normal
// close result is known. Existing software stops tighten on a confirmed regime
// change, but never loosen on BloFin.
func advanceBloFinDynamicCloseRegime(sc StrategyConfig, state *StrategyState, symbol string, mark float64, mu *sync.RWMutex, logger *StrategyLogger) *BloFinResult {
	if sc.Platform != "blofin" || sc.Type != "perps" || state == nil || symbol == "" || mu == nil || !strategyUsesDynamicRegimeClose(sc) {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	pos := state.Positions[symbol]
	if pos == nil || pos.Quantity <= 0 || tpConsumptionHoldsRegime(pos) {
		return nil
	}
	if pos.EntryATR <= 0 {
		if logger != nil {
			logger.InfoOnChange("blofin-dynamic-missing-entry-atr", symbol,
				"BloFin dynamic close for %s requires a valid entry ATR; keeping the previously armed stop trigger", symbol)
		}
		return blofinVirtualStopResult(sc, pos, symbol, mark, pos.StopLossTriggerPx, dynamicCloseStrategyName, "dynamic_regime_stop:missing_entry_atr")
	}
	oldLabel := dynamicCloseATRRegimeLabel(pos, sc)
	changed := advanceDynamicCloseRegime(pos, state, sc)
	label := dynamicCloseATRRegimeLabel(pos, sc)
	if label == "" {
		return nil
	}
	mult, ok := unifiedCloseStopLossATR(sc, label)
	if !ok || mult <= 0 {
		if logger != nil {
			logger.Warn("BloFin dynamic close has no resolved virtual stop for applied regime %q; keeping the previously armed trigger", label)
		}
		return blofinVirtualStopResult(sc, pos, symbol, mark, pos.StopLossTriggerPx, dynamicCloseStrategyName, "dynamic_regime_stop:unresolved_label:"+label)
	}
	candidate := blofinFixedATRTrigger(pos.Side, pos.riskAnchorPrice(), pos.EntryATR, mult)
	if candidate > 0 {
		current := pos.StopLossTriggerPx
		if current <= 0 || (pos.Side == "long" && candidate > current) || (pos.Side == "short" && candidate < current) {
			pos.StopLossTriggerPx = candidate
			if logger != nil && changed {
				logger.Info("BloFin dynamic close regime confirmed %s -> %s; virtual stop tightened to %.6f", oldLabel, label, candidate)
			}
		}
	}
	return blofinVirtualStopResult(sc, pos, symbol, mark, pos.StopLossTriggerPx, dynamicCloseStrategyName, fmt.Sprintf("dynamic_regime_stop:%s:%g", label, mult))
}
