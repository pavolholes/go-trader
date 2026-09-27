package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	portfolioWarningRecentWindow = 15 * time.Minute
	portfolioWarningMaxRows      = 5
	portfolioWarningMaxChars     = 1900
)

type PortfolioWarningMessageInputs struct {
	Reason           string
	Config           *PortfolioRiskConfig
	State            *AppState
	Partition        RiskPartition
	CfgStrategies    []StrategyConfig
	Prices           map[string]float64
	TotalValue       float64
	PerpsLoss        float64
	PerpsMargin      float64
	Recent           []Trade
	Now              time.Time
	EquityGuardArmed bool
}

type portfolioWarningContributor struct {
	ID             string
	PnLLabel       string
	PnL            float64
	DrawdownPct    float64
	PositionLine   string
	NegativeWeight float64
}

func BuildPortfolioWarningMessage(in PortfolioWarningMessageInputs) string {
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	part := in.Partition
	if part.Scope == scopeUnassigned {
		part = livePartition
	}
	var prs PortfolioRiskState
	if in.State != nil {
		if p := in.State.partitionRiskIfPresent(part); p != nil {
			prs = *p
		}
	}
	maxDD := 0.0
	warnDD := 0.0
	if in.Config != nil {
		maxDD = in.Config.MaxDrawdownPct
		warnDD = maxDD * in.Config.WarnThresholdPct / 100
	}

	var b strings.Builder
	b.WriteString("**PORTFOLIO WARNING")
	if in.Partition.Scope != scopeUnassigned {
		b.WriteString(" ")
		b.WriteString(strings.ToUpper(partitionLabel(in.Partition)))
	}
	b.WriteString("**\n")

	if in.EquityGuardArmed {
		note := ""
		if prs.DrawdownReadingSubstituted {
			note = " (balance substituted this cycle)"
		}
		b.WriteString(fmt.Sprintf("Equity drawdown: %.1f%%%s ($%.0f / peak $%.0f).\n",
			prs.CurrentDrawdownPct, note, in.TotalValue, prs.PeakValue))
	} else {
		b.WriteString("Equity drawdown: n/a this cycle.\n")
	}
	if in.PerpsMargin > 0 {
		b.WriteString(fmt.Sprintf("Perps margin drawdown: %.1f%% ($%.0f loss on $%.0f margin).\n",
			prs.CurrentMarginDrawdownPct, in.PerpsLoss, in.PerpsMargin))
	}

	b.WriteString(fmt.Sprintf("Warning threshold (equity or margin): %.1f%%.\n", warnDD))

	switch {
	case !prs.UntrustedOverLimitSince.IsZero():
		b.WriteString(fmt.Sprintf("CRITICAL: equity data is untrusted; the equity kill switch is DEFERRED. Strategy-level circuit breakers remain active. Escalation by %s UTC.",
			prs.UntrustedOverLimitSince.Add(untrustedEquityLatchDeferral).Format("2006-01-02 15:04")))
	case in.EquityGuardArmed:
		b.WriteString(fmt.Sprintf("Equity kill switch: %.1f%%; current equity drawdown %.1f%% (%.1f pp away).",
			maxDD, prs.CurrentDrawdownPct, positiveDistance(maxDD, prs.CurrentDrawdownPct)))
		if in.PerpsMargin > 0 {
			if prs.CurrentMarginDrawdownPct > maxDD {
				b.WriteString(fmt.Sprintf(" Perps margin drawdown %.1f%% exceeds %.1f%%; per-strategy circuit breakers handle margin risk.",
					prs.CurrentMarginDrawdownPct, maxDD))
			} else {
				b.WriteString(fmt.Sprintf(" Perps margin drawdown: %.1f%%.", prs.CurrentMarginDrawdownPct))
			}
		}
		b.WriteString(" Heads-up only; no manual position close is requested.")
	case in.PerpsMargin > 0:
		b.WriteString(fmt.Sprintf("Perps-margin kill switch: %.1f%%; current margin drawdown %.1f%% (%.1f pp away). No manual position close is requested.",
			maxDD, prs.CurrentMarginDrawdownPct, positiveDistance(maxDD, prs.CurrentMarginDrawdownPct)))
		b.WriteString(" Equity is unavailable; perps margin is the active portfolio guard.")
	default:
		b.WriteString("Equity is unavailable and no perps margin is deployed; no manual action is requested.")
	}

	msg := strings.TrimRight(b.String(), "\n")
	return truncateWarningField(msg, portfolioWarningMaxChars)
}

func portfolioWarningContributors(state *AppState, cfgStrategies []StrategyConfig, part RiskPartition, prices map[string]float64, includePaused bool) ([]portfolioWarningContributor, int) {
	if state == nil {
		return nil, 0
	}
	scoped := state.Strategies
	if len(cfgStrategies) > 0 {
		scoped = filterStatesByPartition(state.Strategies, cfgStrategies, part)
	}
	pausedByID := make(map[string]bool, len(cfgStrategies))
	if !includePaused {
		for _, sc := range cfgStrategies {
			if sc.Paused {
				pausedByID[sc.ID] = true
			}
		}
	}
	totalNegative := 0.0
	out := make([]portfolioWarningContributor, 0, len(scoped))
	ids := make([]string, 0, len(scoped))
	for id := range scoped {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	excludedFlatPaused := 0
	for _, id := range ids {
		ss := scoped[id]
		if ss == nil {
			continue
		}
		if pausedByID[id] && !strategyHasOpenPositions(ss) {
			excludedFlatPaused++
			continue
		}
		pv := PortfolioValue(ss, prices)
		initCap := ss.InitialCapital
		pnlLabel := "P&L"
		if ss.SharedWalletPoolBudget || ss.SharedWalletPerformanceOnly {
			pv = displayStrategyValue(ss, prices)
			initCap = 0
			pnlLabel = "net P&L"
		} else if initCap <= 0 {
			initCap = pv - ss.RiskState.DailyPnL
			pnlLabel = "daily P&L"
		}
		pnl := pv - initCap
		if pnl < 0 {
			totalNegative += -pnl
		}
		out = append(out, portfolioWarningContributor{
			ID:           id,
			PnLLabel:     pnlLabel,
			PnL:          pnl,
			DrawdownPct:  ss.RiskState.CurrentDrawdownPct,
			PositionLine: formatPortfolioWarningPosition(ss, prices),
		})
	}
	if totalNegative > 0 {
		for i := range out {
			if out[i].PnL < 0 {
				out[i].NegativeWeight = -out[i].PnL / totalNegative
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].PnL == out[j].PnL {
			return out[i].ID < out[j].ID
		}
		return out[i].PnL < out[j].PnL
	})
	if len(out) > portfolioWarningMaxRows {
		out = out[:portfolioWarningMaxRows]
	}
	return out, excludedFlatPaused
}

func formatPortfolioWarningPosition(ss *StrategyState, prices map[string]float64) string {
	if ss == nil {
		return "(flat)"
	}
	symbols := make([]string, 0, len(ss.Positions))
	for sym, pos := range ss.Positions {
		if pos != nil && pos.Quantity > 0 {
			symbols = append(symbols, sym)
		}
	}
	sort.Strings(symbols)
	if len(symbols) == 0 {
		return "(flat)"
	}
	sym := symbols[0]
	pos := ss.Positions[sym]
	price := prices[sym]
	if price <= 0 {
		price = pos.AvgCost
	}
	pnl := positionUnrealizedPnL(pos, price)
	line := fmt.Sprintf("pos: %s %s %s @ $%s (%s unrealized)", pos.Side, formatWarningQty(pos.Quantity), sym, formatWarningPrice(pos.AvgCost), formatSignedDollar(pnl))
	if len(symbols) > 1 {
		line += fmt.Sprintf(" +%d more", len(symbols)-1)
	}
	return line
}

func positionUnrealizedPnL(pos *Position, price float64) float64 {
	if pos == nil {
		return 0
	}
	mult := pos.Multiplier
	if mult <= 0 {
		mult = 1
	}
	if pos.Side == "short" {
		return pos.Quantity * mult * (pos.AvgCost - price)
	}
	return pos.Quantity * mult * (price - pos.AvgCost)
}

func positiveDistance(limit, current float64) float64 {
	d := limit - current
	if d < 0 {
		return 0
	}
	return d
}

func formatWarningDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh%dm", h, m)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	return fmt.Sprintf("%dd%dh", days, hours)
}

func formatSignedDollar(v float64) string {
	if v < 0 {
		return fmt.Sprintf("-$%.0f", math.Abs(v))
	}
	return fmt.Sprintf("$%.0f", v)
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func formatWarningQty(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", v), "0"), ".")
}

func formatWarningPrice(v float64) string {
	abs := math.Abs(v)
	switch {
	case abs >= 1000:
		return fmt.Sprintf("%.0f", v)
	case abs >= 1:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", v), "0"), ".")
	default:
		return fmt.Sprintf("%.6g", v)
	}
}

func truncateWarningField(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
