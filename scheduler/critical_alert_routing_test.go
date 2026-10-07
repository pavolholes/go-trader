package main

import "testing"

func TestLiveExecFailureRoutesToTradeAlertChannel(t *testing.T) {
	sc := StrategyConfig{ID: "routing-live-failure", Platform: "blofin"}
	clearLiveExecThrottle(sc, "BUY", "XLM")
	defer clearLiveExecThrottle(sc, "BUY", "XLM")
	mock := &mockNotifier{}
	notifier := NewMultiNotifier(notifierBackend{
		notifier:           mock,
		channels:           map[string]string{"default": "info-channel"},
		tradeAlertChannels: map[string]string{"default": "trade-channel"},
	})
	notifyLiveExecFailure(notifier, sc, "BUY", "XLM", "rejected")
	if len(mock.messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(mock.messages))
	}
	if got := mock.messages[0].channelID; got != "trade-channel" {
		t.Fatalf("channel = %q, want trade-channel", got)
	}
}
