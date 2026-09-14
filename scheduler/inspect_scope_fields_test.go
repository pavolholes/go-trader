package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildStrategyInspectionJSONScopeFields(t *testing.T) {
	cfg := &Config{
		IntervalSeconds: 300,
		ATRMethod:       "wilder",
		ReplayLogPath:   "/var/lib/go-trader/shared/replay.db",
		Discord: DiscordConfig{
			Channels:           map[string]string{"hyperliquid": "C-live", "hyperliquid-paper": "C-paper"},
			TradeAlertChannels: map[string]string{"hyperliquid-live": "A-live"},
			DMChannels:         map[string]string{"perps": "D-all"},
		},
		PortfolioRisk: &PortfolioRiskConfig{
			MaxDrawdownPct:  25,
			DailyMaxLossUSD: 500,
			Paper:           &PortfolioRiskConfig{MaxDrawdownPct: 50},
		},
	}
	cases := []struct {
		name          string
		sc            StrategyConfig
		wantScope     PortfolioScope
		wantStorageID string
		wantChannel   string
		wantChKey     string
		wantAlert     string
		wantAlertKey  string
		wantDM        string
		wantDD        float64
		wantDaily     float64
		wantInherits  []string
		wantReplaySrc string
		wantATR       string
	}{
		{
			name: "live strategy",
			sc: StrategyConfig{ID: "hl-x", Type: "perps", Platform: "hyperliquid",
				Args: []string{"vwap", "ETH", "1h", "--mode=live"}, ReplaySharing: "live_mirror"},
			wantScope: ScopeLive, wantStorageID: "hl-x",
			wantChannel: "C-live", wantChKey: "hyperliquid",
			wantAlert: "A-live", wantAlertKey: "hyperliquid-live",
			wantDM: "", wantDD: 25, wantDaily: 500, wantInherits: []string{},
			wantReplaySrc: "", wantATR: "wilder",
		},
		{
			name: "paper strategy with alias and explicit replay source",
			sc: StrategyConfig{ID: "hl-x-paper", StorageStrategyID: "hl-x", Type: "perps", Platform: "hyperliquid",
				Args: []string{"vwap", "ETH", "1h", "--mode=paper"}, ReplaySharing: "live_mirror", ReplaySourceID: "hl-x",
				ATRMethod: "simple"},
			wantScope: ScopePaper, wantStorageID: "hl-x",
			wantChannel: "C-paper", wantChKey: "hyperliquid-paper",
			wantAlert: "C-paper", wantAlertKey: "hyperliquid-paper",
			wantDM: "", wantDD: 50, wantDaily: 500, wantInherits: []string{"daily_max_loss_usd"},
			wantReplaySrc: "hl-x", wantATR: "simple",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := buildStrategyInspectionJSON(tc.sc, nil, cfg, nil)
			raw, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Scope        PortfolioScope `json:"scope"`
				StorageID    string         `json:"storage_strategy_id"`
				ATRMethod    string         `json:"atr_method"`
				Notification struct {
					ChannelKey   string `json:"channel_key"`
					Channel      string `json:"channel"`
					AlertKey     string `json:"trade_alert_key"`
					AlertChannel string `json:"trade_alert_channel"`
					DMKey        string `json:"dm_key"`
					DMChannel    string `json:"dm_channel"`
				} `json:"notification"`
				Replay struct {
					EffectiveSource string `json:"effective_source"`
				} `json:"replay"`
				ScopeRisk struct {
					MaxDrawdownPct  float64  `json:"max_drawdown_pct"`
					DailyMaxLossUSD float64  `json:"daily_max_loss_usd"`
					Inherits        []string `json:"zero_override_inherits"`
				} `json:"scope_risk"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Scope != tc.wantScope || got.StorageID != tc.wantStorageID || got.ATRMethod != tc.wantATR {
				t.Errorf("scope/storage/atr = %s/%s/%s, want %s/%s/%s", got.Scope, got.StorageID, got.ATRMethod, tc.wantScope, tc.wantStorageID, tc.wantATR)
			}
			n := got.Notification
			if n.Channel != tc.wantChannel || n.ChannelKey != tc.wantChKey || n.AlertChannel != tc.wantAlert || n.AlertKey != tc.wantAlertKey || n.DMChannel != tc.wantDM || n.DMKey != "" {
				t.Errorf("notification = %+v", n)
			}
			if got.Replay.EffectiveSource != tc.wantReplaySrc {
				t.Errorf("replay.effective_source = %q, want %q", got.Replay.EffectiveSource, tc.wantReplaySrc)
			}
			if got.ScopeRisk.MaxDrawdownPct != tc.wantDD || got.ScopeRisk.DailyMaxLossUSD != tc.wantDaily {
				t.Errorf("scope_risk = %+v", got.ScopeRisk)
			}
			if strings.Join(got.ScopeRisk.Inherits, ",") != strings.Join(tc.wantInherits, ",") {
				t.Errorf("zero_override_inherits = %v, want %v", got.ScopeRisk.Inherits, tc.wantInherits)
			}
		})
	}
}

func TestNotificationRoutingJSONDMExactKey(t *testing.T) {
	paper := StrategyConfig{Type: "perps", Platform: "hyperliquid", Args: []string{"vwap", "ETH", "1h", "--mode=paper"}}
	live := StrategyConfig{Type: "perps", Platform: "hyperliquid", Args: []string{"vwap", "ETH", "1h", "--mode=live"}}
	channels := map[string]string{"hyperliquid": "C-live", "hyperliquid-paper": "C-paper"}
	alerts := map[string]string{"hyperliquid-live": "A-live"}
	cases := []struct {
		name         string
		sc           StrategyConfig
		dms          map[string]string
		wantDMKey    string
		wantDM       string
		wantChKey    string
		wantChannel  string
		wantAlertKey string
		wantAlert    string
	}{
		{
			name: "paper bare platform is empty", sc: paper,
			dms:       map[string]string{"hyperliquid": "D-live"},
			wantChKey: "hyperliquid-paper", wantChannel: "C-paper",
			wantAlertKey: "hyperliquid-paper", wantAlert: "C-paper",
		},
		{
			name: "paper paper key", sc: paper,
			dms:       map[string]string{"hyperliquid-paper": "D-paper"},
			wantDMKey: "hyperliquid-paper", wantDM: "D-paper",
			wantChKey: "hyperliquid-paper", wantChannel: "C-paper",
			wantAlertKey: "hyperliquid-paper", wantAlert: "C-paper",
		},
		{
			name: "live platform key", sc: live,
			dms:       map[string]string{"hyperliquid": "D-live"},
			wantDMKey: "hyperliquid", wantDM: "D-live",
			wantChKey: "hyperliquid", wantChannel: "C-live",
			wantAlertKey: "hyperliquid-live", wantAlert: "A-live",
		},
		{
			name: "paper type key is empty", sc: paper,
			dms:       map[string]string{"perps": "D-all"},
			wantChKey: "hyperliquid-paper", wantChannel: "C-paper",
			wantAlertKey: "hyperliquid-paper", wantAlert: "C-paper",
		},
		{
			name: "live type key is empty", sc: live,
			dms:       map[string]string{"perps": "D-all"},
			wantChKey: "hyperliquid", wantChannel: "C-live",
			wantAlertKey: "hyperliquid-live", wantAlert: "A-live",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Discord: DiscordConfig{
				Channels:           channels,
				TradeAlertChannels: alerts,
				DMChannels:         tc.dms,
			}}
			got := notificationRoutingJSON(tc.sc, cfg, isLiveArgs(tc.sc.Args))
			if got["dm_key"] != tc.wantDMKey || got["dm_channel"] != tc.wantDM {
				t.Errorf("dm = %v/%v, want %q/%q", got["dm_key"], got["dm_channel"], tc.wantDMKey, tc.wantDM)
			}
			if got["channel_key"] != tc.wantChKey || got["channel"] != tc.wantChannel {
				t.Errorf("channel = %v/%v, want %q/%q", got["channel_key"], got["channel"], tc.wantChKey, tc.wantChannel)
			}
			if got["trade_alert_key"] != tc.wantAlertKey || got["trade_alert_channel"] != tc.wantAlert {
				t.Errorf("alert = %v/%v, want %q/%q", got["trade_alert_key"], got["trade_alert_channel"], tc.wantAlertKey, tc.wantAlert)
			}
		})
	}
}
