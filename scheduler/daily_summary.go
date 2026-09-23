package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// dailySummaryLastSent drzi posledny UTC den, za ktory sa poslal sumar.
// In-memory: po restarte sa neinicializuje hned, prvy sumar pride pri
// dalsom prechode polnoci (neposiela sa spätne).
var dailySummaryLastSent = ""

// maybeSendDailySummary posle raz denne (pri prvom cykle noveho UTC dna)
// suhrn za predchadzajuci den na DISCORD_DAILY_SUMMARY_CHANNEL_ID.
func maybeSendDailySummary(store *StateStore, state *AppState, cfg *Config, notifier *MultiNotifier, now time.Time, prices map[string]float64) {
	ch := strings.TrimSpace(os.Getenv("DISCORD_DAILY_SUMMARY_CHANNEL_ID"))
	if ch == "" || notifier == nil || !notifier.HasBackends() {
		return
	}
	day := now.UTC().Format("2006-01-02")
	if dailySummaryLastSent == "" {
		dailySummaryLastSent = day
		return
	}
	if day == dailySummaryLastSent {
		return
	}
	y := now.UTC().Add(-24 * time.Hour)
	since := time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, time.UTC)
	until := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	msg := buildDailySummary(store, state, cfg, since, until, prices)
	if msg == "" {
		dailySummaryLastSent = day
		return
	}
	if err := notifier.SendMessage(ch, msg); err != nil {
		fmt.Printf("[WARN] daily summary send failed: %v\n", err)
		return
	}
	dailySummaryLastSent = day
	fmt.Printf("[INFO] daily summary posted for %s\n", since.Format("2006-01-02"))
}

type dailyStratRow struct {
	id     string
	pnl    float64
	trades int
	wins   int
}

func buildDailySummary(store *StateStore, state *AppState, cfg *Config, since, until time.Time, prices map[string]float64) string {
	label := strings.TrimSpace(os.Getenv("INSTANCE_LABEL"))
	title := "\U0001F4CA DAILY SUMMARY"
	if label != "" {
		title += " " + strings.ToUpper(label)
	}
	title += " " + since.Format("2006-01-02")

	var closed []ClosedPosition
	if store != nil {
		if rows, _, err := store.QueryClosedPositions("", "", since, until, 100000, 0); err == nil {
			closed = rows
		}
	}
	byStrat := map[string]*dailyStratRow{}
	var dayPnL float64
	var wins, losses int
	for _, c := range closed {
		r := byStrat[c.StrategyID]
		if r == nil {
			r = &dailyStratRow{id: c.StrategyID}
			byStrat[c.StrategyID] = r
		}
		r.pnl += c.RealizedPnL
		r.trades++
		dayPnL += c.RealizedPnL
		if c.RealizedPnL > 0 {
			wins++
			r.wins++
		} else {
			losses++
		}
	}
	rows := make([]*dailyStratRow, 0, len(byStrat))
	for _, r := range byStrat {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].pnl > rows[j].pnl })

	var openCount int
	var totalValue float64
	if state != nil {
		for _, s := range state.Strategies {
			if s == nil {
				continue
			}
			openCount += len(s.Positions) + len(s.OptionPositions)
		}
		totalValue = latestDisplayTotal(state, prices)
	}

	var sb strings.Builder
	sb.WriteString(title + "\n")
	sb.WriteString(fmt.Sprintf("Day PnL: $%.2f | W/L: %d/%d | Closed: %d | Open now: %d | Value: $%.2f\n",
		dayPnL, wins, losses, len(closed), openCount, totalValue))
	n := 3
	if len(rows) < n {
		n = len(rows)
	}
	if n > 0 {
		sb.WriteString("Top: ")
		for i := 0; i < n; i++ {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s ($%.2f)", rows[i].id, rows[i].pnl))
		}
		sb.WriteString("\n")
	}
	if len(rows) > n {
		m := n + 3
		if len(rows) < m {
			m = len(rows)
		}
		sb.WriteString("Bottom: ")
		for i := len(rows) - (m - n); i < len(rows); i++ {
			if i > len(rows)-(m-n) {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s ($%.2f)", rows[i].id, rows[i].pnl))
		}
		sb.WriteString("\n")
	}
	_ = cfg
	return sb.String()
}
