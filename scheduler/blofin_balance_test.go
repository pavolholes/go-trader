package main

import (
	"errors"
	"testing"
)

func TestParseBloFinCopyBalanceOutputRequiresCopyTotalEquity(t *testing.T) {
	cases := []struct {
		name    string
		stdout  string
		runErr  error
		wantErr bool
	}{
		{
			name:    "trusted copy futures equity",
			stdout:  `{"total_equity":390.5,"available":350,"account_type":"copy_trading_futures","mode":"perps","timestamp":"2026-09-27T21:00:00Z","exchange_timestamp_ms":1790533200000}`,
			wantErr: false,
		},
		{
			name:    "standard futures balance is not copy equity",
			stdout:  `{"total_equity":390.5,"account_type":"futures","mode":"perps","timestamp":"2026-09-27T21:00:00Z","exchange_timestamp_ms":1790533200000}`,
			wantErr: true,
		},
		{
			name:    "zero equity is not a trusted snapshot",
			stdout:  `{"total_equity":0,"account_type":"copy_trading_futures","mode":"perps","timestamp":"2026-09-27T21:00:00Z","exchange_timestamp_ms":1790533200000}`,
			wantErr: true,
		},
		{
			name:    "subprocess error",
			stdout:  `{"error":"API unavailable"}`,
			runErr:  errors.New("exit status 1"),
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := parseBloFinCopyBalanceOutput([]byte(tc.stdout), "", tc.runErr)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseBloFinCopyBalanceOutput() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.name == "trusted copy futures equity" && (got == nil || got.TotalEquity != 390.5 || got.ExchangeTimestamp != 1790533200000) {
				t.Fatalf("trusted result = %+v", got)
			}
		})
	}
}
