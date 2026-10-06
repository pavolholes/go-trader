package main

import (
	"strings"
	"testing"
)

func TestBloFinPendingFillAlertIsNotScriptFailure(t *testing.T) {
	sc := StrategyConfig{ID: "live-order_blocks-spcx-15m", Platform: "blofin", Type: "perps"}
	mock := &mockNotifier{}
	notifier := &MultiNotifier{backends: []notifierBackend{{
		notifier: mock, channels: map[string]string{"default": "live-trades-info"}, tradeAlertChannels: map[string]string{"default": "live-trades"},
	}}}

	notifyBloFinOrderFillPending(notifier, sc, "SELL", "SPCX", "17014884", "client-abc")

	if len(mock.messages) != 1 {
		t.Fatalf("pending-fill messages=%d, want 1", len(mock.messages))
	}
	message := mock.messages[0].content
	if got := mock.messages[0].channelID; got != "live-trades-info" {
		t.Fatalf("pending-fill channel = %q, want live-trades-info", got)
	}
	if strings.Contains(message, "SIGNAL SCRIPT FAILING") {
		t.Fatalf("pending venue fill was mislabeled as script failure: %q", message)
	}
	for _, want := range []string{"ORDER AWAITING FILL", sc.ID, "SELL SPCX", "17014884", "client-abc", "has not confirmed a fill", "not a rejection", "may still be pending", "avoid a duplicate"} {
		if !strings.Contains(message, want) {
			t.Errorf("pending-fill message %q is missing %q", message, want)
		}
	}
}
