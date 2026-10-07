package main

import (
	"math"
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
		wantNetPnL float64
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
			wantNetPnL: 0.80,
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
			wantNetPnL: -1.20,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := FormatTradeDM(sc, tc.trade, "live", nil)
			netPnL, known, netOfFees := tradeAlertClosePnL(tc.trade)
			if !known || !netOfFees {
				t.Fatalf("close PnL known=%t netOfFees=%t, want both true", known, netOfFees)
			}
			if math.Abs(netPnL-tc.wantNetPnL) > 1e-9 {
				t.Fatalf("net close fill PnL = %.2f, want %.2f", netPnL, tc.wantNetPnL)
			}
			if !strings.HasPrefix(msg, tc.wantPrefix) {
				t.Fatalf("message header = %q, want prefix %q", msg, tc.wantPrefix)
			}
			if !strings.Contains(msg, tc.wantResult) {
				t.Fatalf("message missing net outcome %q: %s", tc.wantResult, msg)
			}
		})
	}
}
