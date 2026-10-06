package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type closeEvaluatorSupport struct {
	Supported       bool     `json:"supported"`
	RequiredContext []string `json:"required_context"`
	StopOwner       string   `json:"stop_owner"`
	Notes           string   `json:"notes"`
}

type closeSupportMatrix struct {
	SchemaVersion int                                         `json:"schema_version"`
	Platforms     map[string]map[string]closeEvaluatorSupport `json:"platforms"`
}

func readCloseSupportMatrix() (closeSupportMatrix, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return closeSupportMatrix{}, err
	}
	paths := []string{
		filepath.Join(cwd, "shared_strategies", "close", "support_matrix.json"),
		filepath.Join(cwd, "..", "shared_strategies", "close", "support_matrix.json"),
	}
	var readErr error
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			readErr = err
			continue
		}
		var matrix closeSupportMatrix
		if err := json.Unmarshal(data, &matrix); err != nil {
			return closeSupportMatrix{}, fmt.Errorf("decode %s: %w", path, err)
		}
		if matrix.SchemaVersion != 1 || matrix.Platforms == nil {
			return closeSupportMatrix{}, fmt.Errorf("%s: unsupported or empty close support matrix", path)
		}
		return matrix, nil
	}
	return closeSupportMatrix{}, fmt.Errorf("close support matrix not found: %w", readErr)
}

func blofinCloseHasEvaluatorStop(sc StrategyConfig, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, ref := range sc.closeRefs() {
		if strings.ToLower(strings.TrimSpace(ref.Name)) != name {
			continue
		}
		if name == dynamicCloseStrategyName && closeParamsAreUnifiedRegime(ref.Params) {
			return true // validateUnifiedRegimeClose requires stop_loss_atr for every label.
		}
		if (name == "tiered_tp_atr_regime" || name == "tiered_tp_atr_live_regime") && closeParamsAreUnifiedRegime(ref.Params) {
			return true
		}
		if name == "atr_stop" {
			v, err := floatFromAnyChecked(ref.Params["atr_mult"])
			return err == nil && v > 0
		}
		if name == "avwap_stop" {
			return true
		}
		if name == "tiered_tp_atr" || name == "tiered_tp_atr_regime" {
			v, err := floatFromAnyChecked(ref.Params["sl_atr_mult"])
			return err == nil && v > 0
		}
	}
	return false
}

func blofinHasStrategyATRStop(sc StrategyConfig) bool {
	if sc.StopLossATRMult != nil && *sc.StopLossATRMult > 0 {
		return true
	}
	return sc.StopLossATRMultRegime != nil && !sc.StopLossATRMultRegime.IsZero()
}

func blofinCloseParamErrors(sc StrategyConfig, name, prefix string) []string {
	var params map[string]interface{}
	for _, ref := range sc.closeRefs() {
		if strings.EqualFold(strings.TrimSpace(ref.Name), name) {
			params = ref.Params
			break
		}
	}
	allowed := map[string]map[string]bool{
		"tiered_tp_pct":      {"tp_tiers": true, "tiers": true},
		"tiered_tp_atr":      {"tp_tiers": true, "tiers": true, "sl_atr_mult": true},
		"tiered_tp_atr_live": {"tp_tiers": true, "tiers": true, "atr_source": true},
		"time_stop":          {"max_bars": true},
		"atr_stop":           {"atr_mult": true, "atr_source": true},
		"zscore_target":      {"lookback": true, "z_target": true},
		"avwap_stop":         {"buffer_atr_mult": true, "atr_source": true},
	}
	keys, hasRules := allowed[name]
	if !hasRules {
		return nil // Regime and ratchet evaluators have dedicated validators.
	}
	var errs []string
	for key := range params {
		if !keys[key] {
			errs = append(errs, fmt.Sprintf("%s: unknown param %q", prefix, key))
		}
	}
	if name == "tiered_tp_pct" {
		if raw, ok := closeTierListParam(params); ok {
			errs = append(errs, validateBloFinPercentTiers(raw, prefix+".tp_tiers")...)
		}
	}
	if name == "tiered_tp_atr" || name == "tiered_tp_atr_live" {
		if raw, ok := closeTierListParam(params); ok {
			tiers, parseErrs := parseTPTierLadderStrict(raw, prefix+".tp_tiers")
			errs = append(errs, parseErrs...)
			errs = append(errs, validateTPTierLadder(tiers, prefix+".tp_tiers")...)
		}
	}
	if raw, ok := params["atr_source"]; ok {
		source, isString := raw.(string)
		source = strings.ToLower(strings.TrimSpace(source))
		if !isString || (source != "entry" && source != "live") {
			errs = append(errs, fmt.Sprintf("%s.atr_source: must be %q or %q", prefix, "entry", "live"))
		}
	}
	positiveNumber := func(key string, min float64, whole bool, required bool) {
		raw, ok := params[key]
		if !ok {
			if required {
				errs = append(errs, fmt.Sprintf("%s.%s: required and must be > %g", prefix, key, min))
			}
			return
		}
		value, err := floatFromAnyChecked(raw)
		if err != nil || value <= min || (whole && (value != math.Trunc(value) || value >= float64(math.MaxInt64))) {
			if whole {
				errs = append(errs, fmt.Sprintf("%s.%s: must be a whole number > %g", prefix, key, min))
			} else {
				errs = append(errs, fmt.Sprintf("%s.%s: must be > %g", prefix, key, min))
			}
		}
	}
	switch name {
	case "time_stop":
		positiveNumber("max_bars", 0, true, true)
	case "atr_stop":
		positiveNumber("atr_mult", 0, false, true)
	case "zscore_target":
		positiveNumber("lookback", 1, true, true)
		positiveNumber("z_target", 0, false, true)
	case "avwap_stop":
		if raw, ok := params["buffer_atr_mult"]; ok {
			if value, err := floatFromAnyChecked(raw); err != nil || value < 0 {
				errs = append(errs, fmt.Sprintf("%s.buffer_atr_mult: must be >= 0", prefix))
			}
		}
	case "tiered_tp_atr":
		if raw, ok := params["sl_atr_mult"]; ok {
			if value, err := floatFromAnyChecked(raw); err != nil || value <= 0 {
				errs = append(errs, fmt.Sprintf("%s.sl_atr_mult: must be > 0", prefix))
			}
		}
	case "tiered_tp_atr_regime":
		if raw, ok := params["sl_atr_mult"]; ok && !closeParamsAreUnifiedRegime(params) {
			if value, err := floatFromAnyChecked(raw); err != nil || value <= 0 {
				errs = append(errs, fmt.Sprintf("%s.sl_atr_mult: must be > 0", prefix))
			}
		}
	}
	return errs
}

func validateBloFinPercentTiers(raw interface{}, prefix string) []string {
	items, ok := raw.([]interface{})
	if !ok || len(items) == 0 {
		return []string{fmt.Sprintf("%s: must be a non-empty list", prefix)}
	}
	type tier struct{ pct, fraction float64 }
	parsed := make([]tier, 0, len(items))
	var errs []string
	for i, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			errs = append(errs, fmt.Sprintf("%s[%d]: must be an object", prefix, i))
			continue
		}
		pct, pErr := floatFromAnyChecked(m["profit_pct"])
		fraction, fErr := floatFromAnyChecked(m["close_fraction"])
		if pErr != nil || pct <= 0 {
			errs = append(errs, fmt.Sprintf("%s[%d].profit_pct: must be > 0", prefix, i))
		}
		if fErr != nil || fraction <= 0 || fraction > 1 {
			errs = append(errs, fmt.Sprintf("%s[%d].close_fraction: must be in (0, 1]", prefix, i))
		}
		parsed = append(parsed, tier{pct: pct, fraction: fraction})
		for key := range m {
			if key != "profit_pct" && key != "close_fraction" {
				errs = append(errs, fmt.Sprintf("%s[%d]: unknown key %q", prefix, i, key))
			}
		}
	}
	if len(errs) > 0 {
		return errs
	}
	sort.Slice(parsed, func(i, j int) bool { return parsed[i].pct < parsed[j].pct })
	for i := 1; i < len(parsed); i++ {
		if parsed[i].pct <= parsed[i-1].pct {
			errs = append(errs, fmt.Sprintf("%s[%d].profit_pct must be greater than the prior tier", prefix, i))
		}
		if parsed[i].fraction <= parsed[i-1].fraction {
			errs = append(errs, fmt.Sprintf("%s[%d].close_fraction must be greater than the prior cumulative fraction", prefix, i))
		}
	}
	return errs
}

func validateBloFinCloseSupport(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	needsMatrix := false
	for _, sc := range cfg.Strategies {
		if sc.Platform == "blofin" && sc.Type == "perps" && sc.CloseStrategy != nil {
			needsMatrix = true
			break
		}
	}
	if !needsMatrix {
		return nil
	}
	matrix, err := readCloseSupportMatrix()
	if err != nil {
		return []string{fmt.Sprintf("BloFin close support: %v", err)}
	}
	supports := matrix.Platforms["blofin-perps"]
	var errs []string
	for _, sc := range cfg.Strategies {
		if sc.Platform != "blofin" || sc.Type != "perps" || sc.CloseStrategy == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(sc.CloseStrategy.Name))
		prefix := fmt.Sprintf("strategy[%s].close_strategy(%s)", sc.ID, name)
		support, ok := supports[name]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s: unknown close evaluator for BloFin perps (registered evaluator support must be declared in shared_strategies/close/support_matrix.json)", prefix))
			continue
		}
		if !support.Supported {
			errs = append(errs, fmt.Sprintf("%s: unsupported on BloFin perps: %s", prefix, support.Notes))
			continue
		}
		errs = append(errs, blofinCloseParamErrors(sc, name, prefix)...)
		if strategyHasPostTPStopRules(sc) {
			errs = append(errs, fmt.Sprintf("%s: sl_after is not supported on BloFin perps until evaluator-triggered TP consumption is booked; remove sl_after to avoid a silent no-op", prefix))
		}
		hasStrategyStop := blofinHasStrategyATRStop(sc)
		hasEvaluatorStop := blofinCloseHasEvaluatorStop(sc, name)
		if hasStrategyStop && hasEvaluatorStop && (name == "tiered_tp_atr" || name == "tiered_tp_atr_regime") {
			errs = append(errs, fmt.Sprintf("%s: do not configure both close param sl_atr_mult and a strategy-level ATR stop; choose one stop owner", prefix))
		}
		if name == "time_stop" {
			tf := strategyDisplayTimeframe(sc)
			_, fixedDuration := diagTimeframeDuration(tf)
			monthly := len(tf) >= 2 && tf[len(tf)-1] == 'M'
			if !fixedDuration && !monthly {
				errs = append(errs, fmt.Sprintf("%s: time_stop requires a supported strategy timeframe (m/h/d/w/M) so completed bars can be counted", prefix))
			}
		}
		if !blofinIsLive(sc.Args) {
			continue
		}
		switch support.StopOwner {
		case "strategy_atr":
			if !hasStrategyStop {
				errs = append(errs, fmt.Sprintf("%s: this evaluator is TP/time/target-only on BloFin; configure stop_loss_atr_mult or stop_loss_atr_mult_regime as the independent software-managed stop owner", prefix))
			}
		case "close_sl_or_strategy_atr":
			if !hasEvaluatorStop && !hasStrategyStop {
				errs = append(errs, fmt.Sprintf("%s: requires close param sl_atr_mult > 0 or an independent stop_loss_atr_mult / stop_loss_atr_mult_regime", prefix))
			}
		case "unified_or_strategy_atr":
			if !hasEvaluatorStop && !hasStrategyStop {
				errs = append(errs, fmt.Sprintf("%s: a TP-only unified close requires a strategy-level stop_loss_atr_mult or stop_loss_atr_mult_regime", prefix))
			}
		case "unified_virtual_stop":
			if !hasEvaluatorStop {
				errs = append(errs, fmt.Sprintf("%s: unified per-regime stop_loss_atr is required for every regime", prefix))
			}
		case "ratchet_virtual_stop":
			if sc.TrailingStopATRMult == nil || *sc.TrailingStopATRMult <= 0 {
				errs = append(errs, fmt.Sprintf("%s: requires trailing_stop_atr_mult > 0 as its virtual stop owner", prefix))
			}
		case "ratchet_regime_virtual_stop":
			if sc.TrailingStopATRMultRegime == nil || sc.TrailingStopATRMultRegime.IsZero() {
				errs = append(errs, fmt.Sprintf("%s: requires trailing_stop_atr_mult_regime as its virtual stop owner", prefix))
			}
		case "evaluator":
		default:
			errs = append(errs, fmt.Sprintf("%s: invalid support-matrix stop_owner %q", prefix, support.StopOwner))
		}
	}
	return errs
}
