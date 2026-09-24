package main

import (
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---- Summary data structures ----

type UISummaryStrategyRow struct {
	ID          string  `json:"id"`
	Strategy    string  `json:"strategy"`
	Symbol      string  `json:"symbol"`
	Timeframe   string  `json:"timeframe"`
	Direction   string  `json:"direction"`
	PnL         float64 `json:"pnl"`
	PnLPct      float64 `json:"pnl_pct"`
	WinRate     float64 `json:"win_rate"`
	TotalTrades int     `json:"total_trades"`
	Sharpe      float64 `json:"sharpe"`
	Regime      string  `json:"regime"`
}

type UISummaryTypeRow struct {
	AvgCaptureRatio float64 `json:"avg_capture_ratio"`
	Type            string  `json:"type"`
	Total           int     `json:"total"`
	WithTrades      int     `json:"with_trades"`
	TotalPnL        float64 `json:"total_pnl"`
	TotalTrades     int     `json:"total_trades"`
	LongPnL         float64 `json:"long_pnl"`
	ShortPnL        float64 `json:"short_pnl"`
}

type UISummarySymbolRow struct {
	Symbol      string  `json:"symbol"`
	Total       int     `json:"total"`
	WithTrades  int     `json:"with_trades"`
	TotalPnL    float64 `json:"total_pnl"`
	TotalTrades int     `json:"total_trades"`
	LongPnL     float64 `json:"long_pnl"`
	ShortPnL    float64 `json:"short_pnl"`
}

type UISummaryPairRow struct {
	Strategy    string  `json:"strategy"`
	Secondary   string  `json:"secondary"`
	Total       int     `json:"total"`
	WithTrades  int     `json:"with_trades"`
	TotalPnL    float64 `json:"total_pnl"`
	PnLPct      float64 `json:"pnl_pct"`
	TotalTrades int     `json:"total_trades"`
	WinRate     float64 `json:"win_rate"`
}

type UIDailyPnLPoint struct {
	Date   string  `json:"date"`
	PnL    float64 `json:"pnl"`
	Trades int     `json:"trades"`
}

type UISummary struct {
	GeneratedAt         int64                  `json:"generated_at"`
	TotalStrategies     int                    `json:"total_strategies"`
	ActiveToday         int                    `json:"active_today"`
	WithTrades          int                    `json:"with_trades"`
	OpenPositions       int                    `json:"open_positions"`
	TotalPnL            float64                `json:"total_pnl"`
	TotalTrades         int                    `json:"total_trades"`
	LongPnL             float64                `json:"long_pnl"`
	ShortPnL            float64                `json:"short_pnl"`
	TodayPnL            float64                `json:"today_pnl"`
	TodayTrades         int                    `json:"today_trades"`
	TodayWins           int                    `json:"today_wins"`
	TodayLosses         int                    `json:"today_losses"`
	TodayPnLHistory     []UIDailyPnLPoint      `json:"today_pnl_history"`
	TopByPnL            []UISummaryStrategyRow `json:"top_by_pnl"`
	BottomByPnL         []UISummaryStrategyRow `json:"bottom_by_pnl"`
	TopByWinRate        []UISummaryStrategyRow `json:"top_by_winrate"`
	TopByTrades         []UISummaryStrategyRow `json:"top_by_trades"`
	ByType              []UISummaryTypeRow     `json:"by_type"`
	BySymbol            []UISummarySymbolRow   `json:"by_symbol"`
	ByStrategySymbol    []UISummaryPairRow     `json:"by_strategy_symbol"`
	ByStrategyTimeframe []UISummaryPairRow     `json:"by_strategy_timeframe"`
}

var accountBalanceCacheMu sync.RWMutex
var accountBalanceCache []byte
var accountBalanceCacheAt time.Time

func (ss *StatusServer) handleAPIAccountBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	accountBalanceCacheMu.RLock()
	cached := accountBalanceCache
	cachedAt := accountBalanceCacheAt
	accountBalanceCacheMu.RUnlock()
	if cached != nil && time.Since(cachedAt) < 60*time.Second {
		w.Header().Set("Content-Type", "application/json")
		w.Write(cached)
		return
	}
	stdout, _, err := RunPythonScript("shared_scripts/fetch_blofin_copy_balance.py", nil)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	accountBalanceCacheMu.Lock()
	accountBalanceCache = stdout
	accountBalanceCacheAt = time.Now()
	accountBalanceCacheMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write(stdout)
}

func (ss *StatusServer) handleAPIInstance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	label := os.Getenv("INSTANCE_LABEL")
	writeJSON(w, map[string]string{"label": label})
}

func (ss *StatusServer) handleAPISummary(w http.ResponseWriter, r *http.Request) {
	if ss.rejectIfDraining(w) {
		return
	}
	if !ss.requireAPIAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/api/summary" && r.URL.Path != "/api/summary/" {
		http.NotFound(w, r)
		return
	}

	summary := ss.buildSummary()
	writeJSON(w, summary)
}

func (ss *StatusServer) buildSummary() UISummary {
	ss.mu.RLock()
	strats := make(map[string]*StrategyState, len(ss.state.Strategies))
	for k, v := range ss.state.Strategies {
		strats[k] = v
	}
	ss.mu.RUnlock()

	prices := ss.fetchLiveMarkPrices()
	today := time.Now().UTC().Truncate(24 * time.Hour)

	var (
		totalStrats  int
		activeToday  int
		withTrades   int
		openPosCount int
		totalTrades  int
		totalPnL     float64
		longPnL      float64
		shortPnL     float64
		todayPnL     float64
		todayTrades  int
		todayWins    int
		todayLosses  int
	)

	type typeAccum struct {
		total    int
		wTrades  int
		pnl      float64
		trades   int
		longPnL  float64
		shortPnL float64
		winSum   float64
		winCount int
	}
	type symbolAccum struct {
		total    int
		wTrades  int
		pnl      float64
		trades   int
		longPnL  float64
		shortPnL float64
	}

	byType := make(map[string]*typeAccum)
	bySymbol := make(map[string]*symbolAccum)

	var topRows []UISummaryStrategyRow

	for _, sc := range ss.strategies {
		totalStrats++
		snapshot, hasState := strats[sc.ID]
		if !hasState {
			continue
		}

		pv := displayStrategyValue(snapshot, prices)
		initCap := EffectiveInitialCapital(sc, snapshot)
		pnl := pv - initCap
		totalPnL += pnl

		pnlPct := 0.0
		if initCap > 0 {
			pnlPct = pnl / initCap * 100
		}

		// Count open positions
		openPosCount += len(snapshot.Positions)

		// Determine strategy short name from ID prefix (bl-<strategy>-<symbol>-<tf>)
		stratName := strategyDisplayType(sc)
		symbol := strategyDisplaySymbol(sc)
		tf := strategyDisplayTimeframe(sc)
		dir := strategyDisplayDirection(sc)

		// Aggregate by type
		ta, ok := byType[stratName]
		if !ok {
			ta = &typeAccum{}
			byType[stratName] = ta
		}
		ta.total++

		// Aggregate by symbol
		sa, ok := bySymbol[symbol]
		if !ok {
			sa = &symbolAccum{}
			bySymbol[symbol] = sa
		}
		sa.total++

		// Lifetime stats from DB
		lifetime := LifetimeTradeStats{}
		sharpe := 0.0
		if ss.stateDB != nil {
			if stats, err := ss.stateDB.LifetimeTradeStatsForStrategy(sc.ID); err == nil {
				lifetime = stats
			}
			if closed, _, err := ss.stateDB.QueryClosedPositions(sc.ID, "", time.Time{}, time.Time{}, sharpeLookbackLimit, 0); err == nil {
				sharpe = ComputeSharpeRatio(closed, initCap, DefaultAnnualRiskFreeRate)
			}
		}

		trades := lifetime.PositionsOpened
		if trades > 0 {
			withTrades++
			ta.wTrades++
			ta.pnl += pnl
			ta.trades += trades
			sa.wTrades++
			sa.pnl += pnl
			sa.trades += trades

			// Track long/short PnL per symbol
			if strings.EqualFold(dir, "long") {
				longPnL += pnl
				sa.longPnL += pnl
				ta.longPnL += pnl
			} else if strings.EqualFold(dir, "short") {
				shortPnL += pnl
				sa.shortPnL += pnl
				ta.shortPnL += pnl
			} else {
				// bidirectional — approximate: split evenly
				half := pnl / 2
				longPnL += half
				shortPnL += half
				sa.longPnL += half
				sa.shortPnL += half
				ta.longPnL += half
				ta.shortPnL += half
			}

			winRate := 0.0
			if lifetime.Wins+lifetime.Losses > 0 {
				winRate = float64(lifetime.Wins) / float64(lifetime.Wins+lifetime.Losses) * 100
				ta.winSum += winRate
				ta.winCount++
			}

			topRows = append(topRows, UISummaryStrategyRow{
				ID:          sc.ID,
				Strategy:    stratName,
				Symbol:      symbol,
				Timeframe:   tf,
				Direction:   dir,
				PnL:         pnl,
				PnLPct:      pnlPct,
				WinRate:     winRate,
				TotalTrades: trades,
				Sharpe:      sharpe,
				Regime:      snapshot.Regime,
			})
		}

		totalTrades += trades

		// Today's activity
		lastTradeTime := time.Time{}
		if len(snapshot.TradeHistory) > 0 {
			lastTradeTime = snapshot.TradeHistory[len(snapshot.TradeHistory)-1].Timestamp
		}
		if !lastTradeTime.IsZero() && (lastTradeTime.Equal(today) || lastTradeTime.After(today)) {
			activeToday++
		}
	}

	// Today's closed positions PnL
	if ss.stateDB != nil {
		closed, _, err := ss.stateDB.QueryClosedPositions("", "", today, time.Time{}, 0, 0)
		if err == nil {
			for _, cp := range closed {
				todayPnL += cp.RealizedPnL
				todayTrades++
				if cp.RealizedPnL > 0 {
					todayWins++
				} else if cp.RealizedPnL < 0 {
					todayLosses++
				}
			}
		}
	}

	dailyPnLHistory := buildDailyPnLHistory(ss.stateDB, 14)

	// Sort and slice top/bottom
	sort.Slice(topRows, func(i, j int) bool {
		ai := topRows[i].PnL
		aj := topRows[j].PnL
		if math.IsNaN(ai) || math.IsInf(ai, 0) {
			return false
		}
		if math.IsNaN(aj) || math.IsInf(aj, 0) {
			return true
		}
		if ai == aj {
			if topRows[i].WinRate == topRows[j].WinRate {
				return topRows[i].TotalTrades > topRows[j].TotalTrades
			}
			return topRows[i].WinRate > topRows[j].WinRate
		}
		return ai > aj
	})
	top10 := minInt(10, len(topRows))
	topByPnL := append([]UISummaryStrategyRow(nil), topRows[:top10]...)

	bottomByPnL := make([]UISummaryStrategyRow, top10)
	copy(bottomByPnL, topRows[len(topRows)-top10:])
	// Reverse bottom (worst first)
	for i, j := 0, len(bottomByPnL)-1; i < j; i, j = i+1, j-1 {
		bottomByPnL[i], bottomByPnL[j] = bottomByPnL[j], bottomByPnL[i]
	}

	// Top by win rate (min 10 trades)
	var winRateRows []UISummaryStrategyRow
	for _, r := range topRows {
		if r.TotalTrades >= 10 {
			winRateRows = append(winRateRows, r)
		}
	}
	sort.Slice(winRateRows, func(i, j int) bool {
		return winRateRows[i].WinRate > winRateRows[j].WinRate
	})
	topByWinRate := winRateRows[:minInt(10, len(winRateRows))]

	// Top by trades
	sort.Slice(topRows, func(i, j int) bool {
		return topRows[i].TotalTrades > topRows[j].TotalTrades
	})
	topByTrades := topRows[:top10]

	// By type
	var typeRows []UISummaryTypeRow
	for name, ta := range byType {
		typeRows = append(typeRows, UISummaryTypeRow{
			Type:        name,
			Total:       ta.total,
			WithTrades:  ta.wTrades,
			TotalPnL:    ta.pnl,
			TotalTrades: ta.trades,
			LongPnL:     ta.longPnL,
			ShortPnL:    ta.shortPnL,
		})
	}
	sort.Slice(typeRows, func(i, j int) bool {
		return typeRows[i].TotalPnL > typeRows[j].TotalPnL
	})

	// #1147: enrich type rows with trade-diagnostics capture-ratio averages.
	if ss.stateDB != nil {
		diagRows, diagErr := ss.stateDB.TradeDiagnosticsRows("")
		if diagErr == nil && len(diagRows) > 0 {
			typeCapture := make(map[string]*[]float64)
			for _, dr := range diagRows {
				if dr.CaptureRatio == nil {
					continue
				}
				parts := strings.SplitN(dr.StrategyID, "-", 3)
				if len(parts) < 2 {
					continue
				}
				typ := parts[1]
				if typeCapture[typ] == nil {
					slice := make([]float64, 0, 4)
					typeCapture[typ] = &slice
				}
				*typeCapture[typ] = append(*typeCapture[typ], *dr.CaptureRatio)
			}
			if len(typeCapture) > 0 {
				typeCaptureAvg := make(map[string]float64, len(typeCapture))
				for typ, vals := range typeCapture {
					if len(*vals) == 0 {
						continue
					}
					var sum float64
					for _, v := range *vals {
						sum += v
					}
					typeCaptureAvg[typ] = sum / float64(len(*vals))
				}
				for i := range typeRows {
					if avg, ok := typeCaptureAvg[typeRows[i].Type]; ok {
						typeRows[i].AvgCaptureRatio = avg
					}
				}
			}
		}
	}

	type pairKey struct {
		strategy  string
		secondary string
	}
	type pairAccum struct {
		total    int
		wTrades  int
		pnl      float64
		trades   int
		winSum   float64
		winCount int
	}
	byStrategySymbol := make(map[pairKey]*pairAccum)
	byStrategyTimeframe := make(map[pairKey]*pairAccum)

	for _, r := range topRows {
		if r.Strategy == "" {
			continue
		}
		psk := pairKey{strategy: r.Strategy, secondary: r.Symbol}
		ps, ok := byStrategySymbol[psk]
		if !ok {
			ps = &pairAccum{}
			byStrategySymbol[psk] = ps
		}
		ps.total++
		ps.pnl += r.PnL
		ps.trades += r.TotalTrades
		if r.TotalTrades > 0 {
			ps.wTrades++
			ps.winSum += r.WinRate
			ps.winCount++
		}

		ptk := pairKey{strategy: r.Strategy, secondary: r.Timeframe}
		pt, ok := byStrategyTimeframe[ptk]
		if !ok {
			pt = &pairAccum{}
			byStrategyTimeframe[ptk] = pt
		}
		pt.total++
		pt.pnl += r.PnL
		pt.trades += r.TotalTrades
		if r.TotalTrades > 0 {
			pt.wTrades++
			pt.winSum += r.WinRate
			pt.winCount++
		}
	}

	var strategySymbolRows []UISummaryPairRow
	for k, pa := range byStrategySymbol {
		winRate := 0.0
		if pa.winCount > 0 {
			winRate = pa.winSum / float64(pa.winCount)
		}
		capital := float64(pa.total) * 1000
		pnlPct := 0.0
		if capital > 0 {
			pnlPct = pa.pnl / capital * 100
		}
		strategySymbolRows = append(strategySymbolRows, UISummaryPairRow{
			Strategy:    k.strategy,
			Secondary:   k.secondary,
			Total:       pa.total,
			WithTrades:  pa.wTrades,
			TotalPnL:    pa.pnl,
			PnLPct:      pnlPct,
			TotalTrades: pa.trades,
			WinRate:     math.Round(winRate*100) / 100,
		})
	}
	sort.Slice(strategySymbolRows, func(i, j int) bool { return strategySymbolRows[i].TotalPnL > strategySymbolRows[j].TotalPnL })

	var strategyTimeframeRows []UISummaryPairRow
	for k, pa := range byStrategyTimeframe {
		winRate := 0.0
		if pa.winCount > 0 {
			winRate = pa.winSum / float64(pa.winCount)
		}
		capital := float64(pa.total) * 1000
		pnlPct := 0.0
		if capital > 0 {
			pnlPct = pa.pnl / capital * 100
		}
		strategyTimeframeRows = append(strategyTimeframeRows, UISummaryPairRow{
			Strategy:    k.strategy,
			Secondary:   k.secondary,
			Total:       pa.total,
			WithTrades:  pa.wTrades,
			TotalPnL:    pa.pnl,
			PnLPct:      pnlPct,
			TotalTrades: pa.trades,
			WinRate:     math.Round(winRate*100) / 100,
		})
	}
	sort.Slice(strategyTimeframeRows, func(i, j int) bool { return strategyTimeframeRows[i].TotalPnL > strategyTimeframeRows[j].TotalPnL })

	// By symbol
	var symbolRows []UISummarySymbolRow
	for name, sa := range bySymbol {
		symbolRows = append(symbolRows, UISummarySymbolRow{
			Symbol:      name,
			Total:       sa.total,
			WithTrades:  sa.wTrades,
			TotalPnL:    sa.pnl,
			TotalTrades: sa.trades,
			LongPnL:     sa.longPnL,
			ShortPnL:    sa.shortPnL,
		})
	}
	sort.Slice(symbolRows, func(i, j int) bool {
		return symbolRows[i].TotalPnL > symbolRows[j].TotalPnL
	})

	return UISummary{
		GeneratedAt:         time.Now().Unix(),
		TotalStrategies:     totalStrats,
		ActiveToday:         activeToday,
		WithTrades:          withTrades,
		OpenPositions:       openPosCount,
		TotalPnL:            totalPnL,
		TotalTrades:         totalTrades,
		LongPnL:             longPnL,
		ShortPnL:            shortPnL,
		TodayPnL:            todayPnL,
		TodayTrades:         todayTrades,
		TodayWins:           todayWins,
		TodayLosses:         todayLosses,
		TodayPnLHistory:     dailyPnLHistory,
		TopByPnL:            topByPnL,
		BottomByPnL:         bottomByPnL,
		TopByWinRate:        topByWinRate,
		TopByTrades:         topByTrades,
		ByType:              typeRows,
		BySymbol:            symbolRows,
		ByStrategySymbol:    strategySymbolRows,
		ByStrategyTimeframe: strategyTimeframeRows,
	}
}

func buildDailyPnLHistory(st *StateStore, days int) []UIDailyPnLPoint {
	if days <= 0 {
		return []UIDailyPnLPoint{}
	}
	end := time.Now().UTC().Truncate(24 * time.Hour)
	start := end.AddDate(0, 0, -(days - 1))
	out := make([]UIDailyPnLPoint, 0, days)
	for i := 0; i < days; i++ {
		day := start.AddDate(0, 0, i)
		out = append(out, UIDailyPnLPoint{Date: day.Format("2006-01-02")})
	}
	db := st.primary()
	if db == nil || db.db == nil {
		return out
	}
	rows, err := db.db.Query(`SELECT substr(closed_at, 1, 10) AS day, COALESCE(SUM(realized_pnl), 0) AS pnl, COUNT(*) AS trades
		FROM closed_positions
		WHERE closed_at >= ?
		GROUP BY day
		ORDER BY day ASC`, formatTime(start))
	if err != nil {
		return out
	}
	defer rows.Close()
	byDay := make(map[string]UIDailyPnLPoint, days)
	for rows.Next() {
		var day string
		var pnl float64
		var trades int
		if scanErr := rows.Scan(&day, &pnl, &trades); scanErr != nil {
			continue
		}
		byDay[day] = UIDailyPnLPoint{Date: day, PnL: pnl, Trades: trades}
	}
	for i, pt := range out {
		if got, ok := byDay[pt.Date]; ok {
			out[i] = got
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func strategyDisplayType(sc StrategyConfig) string {
	// Extract strategy name from ID prefix (bl-<name>-...)
	parts := strings.SplitN(sc.ID, "-", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return sc.ID
}
