package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const blofinCopyBalanceScript = "shared_scripts/fetch_blofin_copy_balance.py"
const blofinCopyEquitySource = "BloFin Copy Trading totalEquity"

type blofinCopyEquitySnapshot struct {
	TotalEquity       float64
	ExchangeTimestamp int64
	FetchedAt         time.Time
}

func defaultBloFinCopyEquitySnapshot() (*blofinCopyEquitySnapshot, error) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("BLOFIN_TRADE_ACCOUNT"))) != "copy" {
		return nil, fmt.Errorf("BLOFIN_TRADE_ACCOUNT must be set to copy for Copy Trading equity risk")
	}
	result, stderr, err := RunBloFinCopyBalance(blofinCopyBalanceScript, "perps")
	if stderr != "" {
		fmt.Fprintf(os.Stderr, "[blofin-balance] stderr: %s\n", stderr)
	}
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("BloFin Copy balance fetch returned no result")
	}
	return &blofinCopyEquitySnapshot{
		TotalEquity:       result.TotalEquity,
		ExchangeTimestamp: result.ExchangeTimestamp,
		FetchedAt:         time.Now().UTC(),
	}, nil
}

func blofinRiskTransitionNotices(sr *scopeCycleRisk) []string {
	if sr == nil || !sr.UsesBloFinEquity {
		return nil
	}
	var notices []string
	if sr.EquityFeedUnavailable {
		notices = append(notices, "**BLOFIN EQUITY FEED UNAVAILABLE**\nCopy Trading `totalEquity` could not be read. New/increasing exposure is held; existing positions and bot-managed exits continue. Trading entries resume after a trusted account snapshot.")
	}
	if sr.EquityBaselineCreated {
		notices = append(notices, fmt.Sprintf("**BLOFIN EQUITY BASELINE INITIALIZED**\nSource: %s\nNew high-water mark: $%.2f; tracked drawdown starts at 0%% from this trusted snapshot. The previous virtual-book peak was discarded.", sr.RiskEquitySource, sr.TotalPV))
	}
	if sr.EquityFeedRecovered && !sr.EquityBaselineCreated {
		notices = append(notices, fmt.Sprintf("**BLOFIN EQUITY FEED RESTORED**\nTrusted %s snapshot received: $%.2f. New/increasing exposure may resume if the account is below its risk thresholds.", sr.RiskEquitySource, sr.TotalPV))
	}
	if sr.KillSwitchActivated && sr.Prs != nil {
		notices = append(notices, fmt.Sprintf("⚠️ **BLOFIN PORTFOLIO ENTRY HALT**\nSource: %s\nEquity drawdown: %.1f%% ($%.2f / peak $%.2f). New/increasing exposure is blocked; no existing position is automatically closed. Bot-managed exits, partial closes, and stop management continue. Auto-rearm requires 3 consecutive trusted readings below %.1f%%.",
			sr.RiskEquitySource, sr.Prs.CurrentDrawdownPct, sr.TotalPV, sr.Prs.PeakValue, portfolioRearmThresholdPct(sr.Config)))
	}
	if sr.KillSwitchRearmed && sr.Prs != nil {
		notices = append(notices, fmt.Sprintf("**BLOFIN PORTFOLIO ENTRY HALT RE-ARMED**\nSource: %s\nThree consecutive trusted readings were below %.1f%% drawdown. Current drawdown: %.1f%% ($%.2f / peak $%.2f). New entries are eligible again.",
			sr.RiskEquitySource, portfolioRearmThresholdPct(sr.Config), sr.Prs.CurrentDrawdownPct, sr.TotalPV, sr.Prs.PeakValue))
	}
	return notices
}
