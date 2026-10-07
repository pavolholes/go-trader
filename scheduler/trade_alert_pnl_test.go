package main

import (
	"strings"
	"testing"
)

func TestFormatTradeDMShowsNetCloseOutcomeAndIcon(t *testing.T) {
	sc := StrategyConfig{ID: "live-test-xlm-15m", Platform: "blofin", Type: "perps"}
	cases := []struct {
		name       string
		trade      Trade
		wantPrefix string
		wantResult string
	}{
		{
			name: "profitable partial is green and net of fee",
			trade: Trade{
				Symbol: "XLM", Side: "sell", Quantity: 10, Price: 0.22, Value: 22,
				Details: "BloFin incremental Copy partial-close fill recovery",
				IsClose: true, PnLGross: true, RealizedPnL: 1.00, ExchangeFee: 0.20,
			},
			wantPrefix: "🟢 **TRADE PARTIAL - LIVE**",
			wantResult: "Close fill result: PROFIT (+0.80 USDT net of fees)",
		},
		{
			name: "losing full close is red and net of fee",
			trade: Trade{
				Symbol: "ETH", Side: "sell", Quantity: 1, Price: 2700, Value: 2700,
				Details: "BloFin incremental Copy close fill recovery",
				IsClose: true, PnLGross: true, RealizedPnL: -1.00, ExchangeFee: 0.20,
			},
			wantPrefix: "🔴 **TRADE CLOSED - LIVE**",
			wantResult: "Close fill result: LOSS (-1.20 USDT net of fees)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := FormatTradeDM(sc, tc.trade, "live", nil)
			if !strings.HasPrefix(msg, tc.wantPrefix) {
				t.Fatalf("message header = %q, want prefix %q", msg, tc.wantPrefix)
			}
			if !strings.Contains(msg, tc.wantResult) {
				t.Fatalf("message missing net outcome %q: %s", tc.wantResult, msg)
			}
		})
	}
}
