package main

// Pavol fork: config validation tests for BloFin/leverage/spot rules and
// Discord channel env defaults. Kept across upstream merges — upstream
// removed most unit tests in #1597, but these guard fork overrides
// (see docs/GO-TRADER_OVERRIDES_PAVOL.md).

import (
	"strings"
	"testing"
)

func TestLoadConfigPerpsLeverageDefault(t *testing.T) {
	dir := t.TempDir()
	cfgJSON := `{
		"strategies": [{
			"id": "hl-test-eth",
			"type": "perps",
			"platform": "hyperliquid",
			"script": "shared_scripts/check_hyperliquid.py",
			"args": ["sma_crossover", "ETH", "1h", "--mode=paper"],
			"capital": 1000
		}]
	}`
	path := writeTestConfig(t, dir, cfgJSON)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	sc := cfg.Strategies[0]
	if sc.Leverage != 1 {
		t.Errorf("Leverage = %g, want 1 (default)", sc.Leverage)
	}
}

// #254: explicit perps Leverage is preserved.
func TestLoadConfigPerpsLeverageExplicit(t *testing.T) {
	dir := t.TempDir()
	cfgJSON := `{
		"strategies": [{
			"id": "hl-test-eth",
			"type": "perps",
			"platform": "hyperliquid",
			"script": "shared_scripts/check_hyperliquid.py",
			"args": ["sma_crossover", "ETH", "1h", "--mode=paper"],
			"capital": 1000,
			"leverage": 10
		}]
	}`
	path := writeTestConfig(t, dir, cfgJSON)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Strategies[0].Leverage != 10 {
		t.Errorf("Leverage = %g, want 10", cfg.Strategies[0].Leverage)
	}
	if cfg.Strategies[0].SizingLeverage != 10 {
		t.Errorf("SizingLeverage = %g, want 10 (defaults to leverage)", cfg.Strategies[0].SizingLeverage)
	}
}

// #497: sizing_leverage can differ from exchange leverage.
func TestLoadConfigPerpsSizingLeverageExplicit(t *testing.T) {
	dir := t.TempDir()
	cfgJSON := `{
		"strategies": [{
			"id": "hl-test-eth",
			"type": "perps",
			"platform": "hyperliquid",
			"script": "shared_scripts/check_hyperliquid.py",
			"args": ["sma_crossover", "ETH", "1h", "--mode=paper"],
			"capital": 1000,
			"leverage": 20,
			"sizing_leverage": 2
		}]
	}`
	path := writeTestConfig(t, dir, cfgJSON)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	sc := cfg.Strategies[0]
	if got := EffectiveExchangeLeverage(sc); got != 20 {
		t.Errorf("EffectiveExchangeLeverage = %g, want 20", got)
	}
	if got := EffectiveSizingLeverage(sc); got != 2 {
		t.Errorf("EffectiveSizingLeverage = %g, want 2", got)
	}
}

// #254: Leverage must be rejected on non-perps types.
func TestLoadConfigLeverageRejectsSpot(t *testing.T) {
	dir := t.TempDir()
	cfgJSON := `{
		"strategies": [{
			"id": "test-spot",
			"type": "spot",
			"script": "shared_scripts/check_strategy.py",
			"args": ["sma_crossover", "BTC/USDT", "1h"],
			"capital": 1000,
			"leverage": 5
		}]
	}`
	path := writeTestConfig(t, dir, cfgJSON)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected validation error for leverage on spot strategy")
	}
	if !strings.Contains(err.Error(), "leverage is only supported for perps") {
		t.Errorf("error = %v, want 'leverage is only supported for perps'", err)
	}
}

// #254: Leverage must be in [1, 150].
func TestLoadConfigLeverageRejectsOutOfRange(t *testing.T) {
	dir := t.TempDir()
	cfgJSON := `{
		"strategies": [{
			"id": "hl-test-eth",
			"type": "perps",
			"platform": "hyperliquid",
			"script": "shared_scripts/check_hyperliquid.py",
			"args": ["sma_crossover", "ETH", "1h", "--mode=paper"],
			"capital": 1000,
			"leverage": 200
		}]
	}`
	path := writeTestConfig(t, dir, cfgJSON)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected validation error for leverage=200")
	}
	if !strings.Contains(err.Error(), "leverage must be in") {
		t.Errorf("error = %v, want 'leverage must be in'", err)
	}
}

func TestLoadConfigSizingLeverageRejectsSpot(t *testing.T) {
	dir := t.TempDir()
	cfgJSON := `{
		"strategies": [{
			"id": "test-spot",
			"type": "spot",
			"script": "shared_scripts/check_strategy.py",
			"args": ["sma_crossover", "BTC/USDT", "1h"],
			"capital": 1000,
			"sizing_leverage": 2
		}]
	}`
	path := writeTestConfig(t, dir, cfgJSON)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected validation error for sizing_leverage on spot strategy")
	}
	if !strings.Contains(err.Error(), "sizing_leverage is only supported for perps") {
		t.Errorf("error = %v, want 'sizing_leverage is only supported for perps'", err)
	}
}

// #497: fractional sizing_leverage is valid — high exchange leverage with
// conservative position size (e.g. leverage=20, sizing_leverage=0.5) is the
// motivating use case for decoupling.
func TestLoadConfigSizingLeverageAcceptsFractional(t *testing.T) {
	dir := t.TempDir()
	cfgJSON := `{
		"strategies": [{
			"id": "hl-test-eth",
			"type": "perps",
			"platform": "hyperliquid",
			"script": "shared_scripts/check_hyperliquid.py",
			"args": ["sma_crossover", "ETH", "1h", "--mode=paper"],
			"capital": 1000,
			"leverage": 20,
			"sizing_leverage": 0.5
		}]
	}`
	path := writeTestConfig(t, dir, cfgJSON)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig with sizing_leverage=0.5 failed: %v", err)
	}
	if got := cfg.Strategies[0].SizingLeverage; got != 0.5 {
		t.Errorf("SizingLeverage = %g, want 0.5", got)
	}
}

func TestLoadConfigSizingLeverageRejectsOutOfRange(t *testing.T) {
	dir := t.TempDir()
	cfgJSON := `{
		"strategies": [{
			"id": "hl-test-eth",
			"type": "perps",
			"platform": "hyperliquid",
			"script": "shared_scripts/check_hyperliquid.py",
			"args": ["sma_crossover", "ETH", "1h", "--mode=paper"],
			"capital": 1000,
			"leverage": 20,
			"sizing_leverage": 200
		}]
	}`
	path := writeTestConfig(t, dir, cfgJSON)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected validation error for sizing_leverage=200")
	}
	if !strings.Contains(err.Error(), "sizing_leverage must be in") {
		t.Errorf("error = %v, want 'sizing_leverage must be in'", err)
	}
}
func TestLoadConfigDiscordChannelEnvDefaults(t *testing.T) {
	t.Setenv("DISCORD_DAILY_SUMMARY_CHANNEL_ID", "ch-daily")
	t.Setenv("DISCORD_TRADES_CHANNEL_ID", "ch-trades")
	t.Setenv("DISCORD_CHANNEL_ID", "ch-legacy")
	q := string(rune(34))
	cfgJSON := "{" + q + "strategies" + q + ": [{"
	cfgJSON += q + "id" + q + ": " + q + "x" + q + ", "
	cfgJSON += q + "type" + q + ": " + q + "spot" + q + ", "
	cfgJSON += q + "script" + q + ": " + q + "s.py" + q + ", "
	cfgJSON += q + "args" + q + ": [" + q + "a" + q + "], "
	cfgJSON += q + "capital" + q + ": 1}]}"
	dir := t.TempDir()
	path := writeTestConfig(t, dir, cfgJSON)
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.Discord.Channels["default"] != "ch-daily" {
		t.Errorf("Channels[default] = %q, want ch-daily", loaded.Discord.Channels["default"])
	}
	if loaded.Discord.TradeAlertChannels["default"] != "ch-trades" {
		t.Errorf("TradeAlertChannels[default] = %q, want ch-trades", loaded.Discord.TradeAlertChannels["default"])
	}
	t.Setenv("DISCORD_TRADES_CHANNEL_ID", "")
	loaded2, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded2.Discord.TradeAlertChannels["default"] != "ch-legacy" {
		t.Errorf("legacy alias: got %q, want ch-legacy", loaded2.Discord.TradeAlertChannels["default"])
	}
}
