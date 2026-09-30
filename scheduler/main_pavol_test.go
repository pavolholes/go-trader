package main

// Pavol fork: trade-alert routing (channel-only, BloFin) and circuit-breaker
// notification tests. Kept across upstream merges (upstream #1597).
// See docs/GO-TRADER_OVERRIDES_PAVOL.md.

import (
	"strings"
	"sync"
	"testing"
	"time"
)
func TestNotifyPerStrategyCircuitBreaker_BroadcastsFreshTriggers(t *testing.T) {
	cases := []struct {
		name   string
		reason string
	}{
		{
			name: "max drawdown",
			reason: RiskReasonMaxDrawdownExceeded +
				" (30.0% > 25.0%, portfolio=$700.00 peak=$1000.00, denom=peak=$1000.00)",
		},
		{
			name:   "consecutive losses",
			reason: RiskReasonConsecutiveLosses + " (5 in a row, threshold 5)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockNotifier{}
			notifier := &MultiNotifier{
				backends: []notifierBackend{
					{
						notifier: mock,
						ownerID:  "owner123",
						channels: map[string]string{
							"spot":        "ch-spot",
							"hyperliquid": "ch-hl",
						},
					},
				},
			}
			sc := StrategyConfig{ID: "test-strategy", Platform: "binanceus", Type: "spot", Args: []string{"sma_cross", "BTC/USDT", "30m"}}

			notifyPerStrategyCircuitBreaker(sc, tc.reason, 1234.56, notifier, false)

			if len(mock.messages) != 2 {
				t.Fatalf("expected 2 channel messages, got %d", len(mock.messages))
			}
			if len(mock.dms) != 0 {
				t.Fatalf("informational circuit-breaker alerts must not use DMs, got %d", len(mock.dms))
			}
			for _, msg := range []string{mock.messages[0].content, mock.messages[1].content} {
				if !strings.Contains(msg, "**CIRCUIT BREAKER**") ||
					!strings.Contains(msg, "[test-strategy]") ||
					!strings.Contains(msg, "Trigger:") ||
					!strings.Contains(msg, "Portfolio impact:") ||
					!strings.Contains(msg, "BinanceUS, BTC, 30m, sma_cross, spot") {
					t.Fatalf("notification missing required context: %q", msg)
				}
				if strings.HasPrefix(tc.reason, RiskReasonConsecutiveLosses) && !strings.Contains(msg, "consecutive losses (5 in a row, threshold 5)") {
					t.Fatalf("expected consecutive-loss trigger in %q", msg)
				}
				if strings.HasPrefix(tc.reason, RiskReasonMaxDrawdownExceeded) && !strings.Contains(msg, "30.0% > 25.0%") {
					t.Fatalf("expected parsed max-drawdown numbers in %q", msg)
				}
			}
		})
	}
}

func TestNotifyPerStrategyCircuitBreaker_SuppressesNonFreshAndPortfolioKill(t *testing.T) {
	cases := []struct {
		name                string
		reason              string
		portfolioKillFired  bool
		notifierHasBackends bool
		nilNotifier         bool
		wantChannelMessages int
		wantOwnerDMs        int
	}{
		{
			name:                "latched circuit breaker no spam",
			reason:              RiskReasonCircuitBreakerActive,
			notifierHasBackends: true,
		},
		{
			name:                "unknown reason strings are dropped",
			reason:              "daily loss limit exceeded",
			notifierHasBackends: true,
		},
		{
			name:                "portfolio kill owns notification",
			reason:              RiskReasonMaxDrawdownExceeded + " (30.0% > 25.0%)",
			portfolioKillFired:  true,
			notifierHasBackends: true,
		},
		{
			name:                "no backends",
			reason:              RiskReasonConsecutiveLosses,
			notifierHasBackends: false,
		},
		{
			name:        "nil notifier",
			reason:      RiskReasonConsecutiveLosses,
			nilNotifier: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockNotifier{}
			var notifier *MultiNotifier
			if !tc.nilNotifier {
				notifier = &MultiNotifier{}
				if tc.notifierHasBackends {
					notifier.backends = []notifierBackend{
						{
							notifier: mock,
							ownerID:  "owner123",
							channels: map[string]string{"spot": "ch-spot"},
						},
					}
				}
			}
			sc := StrategyConfig{ID: "test-strategy", Platform: "binanceus", Type: "spot"}

			notifyPerStrategyCircuitBreaker(sc, tc.reason, 1234.56, notifier, tc.portfolioKillFired)

			if len(mock.messages) != tc.wantChannelMessages {
				t.Fatalf("channel messages = %d, want %d", len(mock.messages), tc.wantChannelMessages)
			}
			if len(mock.dms) != tc.wantOwnerDMs {
				t.Fatalf("owner DMs = %d, want %d", len(mock.dms), tc.wantOwnerDMs)
			}
		})
	}
}

func testTrade() Trade {
	return Trade{
		Timestamp:  time.Now(),
		StrategyID: "test-spot-sma",
		Symbol:     "BTC/USDT",
		Side:       "buy",
		Quantity:   0.01,
		Price:      50000,
		Value:      500,
		TradeType:  "spot",
		Details:    "Open long BTC/USDT",
	}
}

func TestSendTradeAlertsRouting(t *testing.T) {
	spotPaper := StrategyConfig{
		ID:       "test-spot-sma",
		Type:     "spot",
		Platform: "binanceus",
		Args:     []string{"sma", "BTC/USDT", "1h", "--mode=paper"},
	}
	hlPaper := StrategyConfig{
		ID:       "hl-sma-btc",
		Type:     "perps",
		Platform: "hyperliquid",
		Args:     []string{"sma", "BTC", "1h", "--mode=paper"},
	}
	hlLive := StrategyConfig{
		ID:       "hl-sma-btc",
		Type:     "perps",
		Platform: "hyperliquid",
		Args:     []string{"sma", "BTC", "1h", "--mode=live"},
	}
	blofinLive := StrategyConfig{
		ID:       "live-order_blocks-spcx-15m",
		Type:     "perps",
		Platform: "blofin",
		Args:     []string{"order_blocks", "SPCX", "15m", "--mode=live"},
	}
	hlPaperNoChannelKey := StrategyConfig{
		ID:       "hl-perps-sma",
		Type:     "perps",
		Platform: "hyperliquid",
		Args:     []string{"sma", "BTC", "1h", "--mode=paper"},
	}

	cases := []struct {
		name               string
		sc                 StrategyConfig
		channels           map[string]string
		dmChannels         map[string]string
		tradeAlertChannels map[string]string
		trades             []Trade
		wantChanIDs        []string
	}{
		{
			name: "DM route config still posts to the channel only", sc: spotPaper,
			channels:    map[string]string{"spot": "ch-spot-123"},
			dmChannels:  map[string]string{"binanceus-paper": "owner123"},
			wantChanIDs: []string{"ch-spot-123"},
		},
		{
			name: "DM-only config does not produce a private alert", sc: spotPaper,
			channels:    map[string]string{},
			dmChannels:  map[string]string{"binanceus-paper": "owner123"},
			wantChanIDs: nil,
		},
		{
			name: "channel only", sc: spotPaper,
			channels:    map[string]string{"spot": "ch-spot-123"},
			wantChanIDs: []string{"ch-spot-123"},
		},
		{
			name: "no configured channel", sc: spotPaper,
			channels:    map[string]string{},
			wantChanIDs: nil,
		},
		{
			name: "no channel for platform", sc: hlPaperNoChannelKey,
			channels:    map[string]string{"spot": "ch-spot-123"},
			wantChanIDs: nil,
		},
		{
			name: "live channel routing ignores DM config", sc: hlLive,
			channels:    map[string]string{"hyperliquid": "ch-hl", "hyperliquid-live": "ch-hl-live"},
			dmChannels:  map[string]string{"hyperliquid": "owner123"},
			wantChanIDs: []string{"ch-hl", "ch-hl-live"},
		},
		{
			name: "BloFin live partial close uses trade-alert channel", sc: blofinLive,
			tradeAlertChannels: map[string]string{"default": "live-trades"},
			trades:             []Trade{{StrategyID: blofinLive.ID, Symbol: "SPCX", Side: "sell", Quantity: 201, Price: 148.83, IsClose: true, RealizedPnL: 0.1809}},
			wantChanIDs:        []string{"live-trades"},
		},
		{
			name: "live channel dedup", sc: hlLive,
			channels:    map[string]string{"hyperliquid": "ch-hl", "hyperliquid-live": "ch-hl"},
			wantChanIDs: []string{"ch-hl"},
		},
		{
			name: "paper takes no live channel", sc: hlPaper,
			channels:    map[string]string{"hyperliquid": "ch-hl", "hyperliquid-live": "ch-hl-live"},
			wantChanIDs: []string{"ch-hl"},
		},
		{
			name: "paper channel routing", sc: hlPaper,
			channels:    map[string]string{"hyperliquid": "ch-hl-live", "hyperliquid-paper": "ch-hl-paper"},
			wantChanIDs: []string{"ch-hl-paper"},
		},
		{
			name: "paper falls back to base channel", sc: hlPaper,
			channels:    map[string]string{"hyperliquid": "ch-hl"},
			wantChanIDs: []string{"ch-hl"},
		},
		{
			name: "paper DM-only route is ignored", sc: hlPaper,
			dmChannels:  map[string]string{"hyperliquid-paper": "user-paper-dm"},
			wantChanIDs: nil,
		},
		{
			name: "live DM-only route is ignored", sc: hlLive,
			dmChannels:  map[string]string{"hyperliquid": "user-live-dm"},
			wantChanIDs: nil,
		},
		{
			name: "dm key missing for mode", sc: hlPaper,
			dmChannels:  map[string]string{"hyperliquid": "only-live"},
			channels:    map[string]string{},
			wantChanIDs: nil,
		},
		{
			name: "channel route works without any DM route", sc: hlPaper,
			channels:    map[string]string{"hyperliquid-paper": "paper-alerts"},
			dmChannels:  map[string]string{"hyperliquid-paper": "private-log-channel"},
			wantChanIDs: []string{"paper-alerts"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockNotifier{}
			trades := tc.trades
			if trades == nil {
				trades = []Trade{testTrade()}
			}
			state := &StrategyState{TradeHistory: trades}
			var mu sync.RWMutex
			notifier := &MultiNotifier{
				backends: []notifierBackend{
					{
						notifier:           mock,
						channels:           tc.channels,
						dmChannels:         tc.dmChannels,
						tradeAlertChannels: tc.tradeAlertChannels,
					},
				},
			}

			sendTradeAlerts(tc.sc, state, 1, &mu, notifier, nil)

			if len(mock.dms) != 0 {
				t.Errorf("trade alerts must never use private DMs, got %#v", mock.dms)
			}
			if len(mock.messages) != len(tc.wantChanIDs) {
				t.Fatalf("channel messages = %d, want %d (%#v)", len(mock.messages), len(tc.wantChanIDs), mock.messages)
			}
			got := map[string]int{}
			for _, m := range mock.messages {
				got[m.channelID]++
			}
			for _, want := range tc.wantChanIDs {
				if got[want] == 0 {
					t.Errorf("expected a message to %s, got %#v", want, mock.messages)
					continue
				}
				got[want]--
			}
		})
	}
}
