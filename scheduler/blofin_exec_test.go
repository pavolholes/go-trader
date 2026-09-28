package main

import (
	"strings"
	"testing"
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
