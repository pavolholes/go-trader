package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"
	"time"
)

type BloFinPosition struct {
	Coin          string
	Size          float64
	EntryPrice    float64
	Side          string
	UnrealizedPnL float64
}

var blofinLiveCloseScript = "shared_scripts/close_blofin_position.py"
var blofinFetchPositionsScript = "shared_scripts/fetch_blofin_positions.py"
var blofinFetchBalanceScript = "shared_scripts/fetch_blofin_balance.py"

type BloFinLiveCloser func(symbol string, partialSz *float64) (*BloFinCloseResult, error)

func defaultBloFinLiveCloser(symbol string, partialSz *float64) (*BloFinCloseResult, error) {
	result, stderr, err := RunBloFinClose(blofinLiveCloseScript, symbol, partialSz)
	if stderr != "" {
		fmt.Fprintf(os.Stderr, "[blofin-close] %s stderr: %s\n", symbol, stderr)
	}
	return result, err
}

type BloFinPositionsFetcher func() ([]BloFinPosition, error)

func defaultBloFinPositionsFetcher() ([]BloFinPosition, error) {
	result, stderr, err := RunBloFinFetchPositions(blofinFetchPositionsScript)
	if stderr != "" {
		fmt.Fprintf(os.Stderr, "[blofin-close] fetch_positions stderr: %s\n", stderr)
	}
	if err != nil {
		return nil, err
	}
	positions := make([]BloFinPosition, 0, len(result.Positions))
	for _, p := range result.Positions {
		positions = append(positions, BloFinPosition{
			Coin:          p.Coin,
			Size:          p.Size,
			EntryPrice:    p.EntryPrice,
			Side:          p.Side,
			UnrealizedPnL: p.UnrealizedPnL,
		})
	}
	return positions, nil
}

type BloFinLiveCloseReport struct {
	ClosedCoins  []string
	AlreadyFlat  []string
	Unconfigured []BloFinPosition
	Errors       map[string]error
}

func (r BloFinLiveCloseReport) ConfirmedFlat() bool {
	return len(r.Errors) == 0
}

func (r BloFinLiveCloseReport) SortedErrorCoins() []string {
	coins := make([]string, 0, len(r.Errors))
	for c := range r.Errors {
		coins = append(coins, c)
	}
	sort.Strings(coins)
	return coins
}

func forceCloseBloFinLive(ctx context.Context, positions []BloFinPosition, blofinLiveAll []StrategyConfig, closer BloFinLiveCloser) BloFinLiveCloseReport {
	report := BloFinLiveCloseReport{Errors: make(map[string]error)}

	tradedCoins := make(map[string]bool)
	for _, sc := range blofinLiveAll {
		if sc.Type != "perps" {
			continue
		}
		sym := blofinSymbol(sc.Args)
		if sym != "" {
			tradedCoins[sym] = true
		}
	}

	for _, p := range positions {
		if !tradedCoins[p.Coin] {
			if p.Size != 0 {
				report.Unconfigured = append(report.Unconfigured, p)
			}
			continue
		}
		if p.Size == 0 {
			report.AlreadyFlat = append(report.AlreadyFlat, p.Coin)
			continue
		}
		if err := ctx.Err(); err != nil {
			report.Errors[p.Coin] = fmt.Errorf("close budget exhausted before submit: %w", err)
			continue
		}
		result, err := closer(p.Coin, nil)
		if err != nil {
			report.Errors[p.Coin] = err
			continue
		}
		if result != nil && result.Close != nil && result.Close.AlreadyFlat {
			report.AlreadyFlat = append(report.AlreadyFlat, p.Coin)
			continue
		}
		report.ClosedCoins = append(report.ClosedCoins, p.Coin)
	}

	return report
}

func blofinLiveStrategiesForCoin(coin string, blofinLiveAll []StrategyConfig) []StrategyConfig {
	var out []StrategyConfig
	for _, sc := range blofinLiveAll {
		if sc.Platform != "blofin" || sc.Type != "perps" {
			continue
		}
		if blofinSymbol(sc.Args) == coin {
			out = append(out, sc)
		}
	}
	return out
}

func blofinStrategyCapitalWeight(sc StrategyConfig) float64 {
	if sc.CapitalPct > 0 {
		return sc.CapitalPct
	}
	if sc.Capital > 0 {
		return sc.Capital
	}
	return 1.0
}

func blofinStrategyCapitalWeights(peers []StrategyConfig) []float64 {
	hasPct := false
	hasAbs := false
	for _, p := range peers {
		switch {
		case p.CapitalPct > 0:
			hasPct = true
		case p.Capital > 0:
			hasAbs = true
		}
	}
	mixed := hasPct && hasAbs
	out := make([]float64, len(peers))
	for i, p := range peers {
		if mixed {
			out[i] = 1.0
			continue
		}
		out[i] = blofinStrategyCapitalWeight(p)
	}
	return out
}

func computeBloFinCircuitCloseQty(coin, strategyID string, blofinPositions []BloFinPosition, blofinLiveAll []StrategyConfig) (qty float64, ok bool) {
	var onChain float64
	found := false
	for i := range blofinPositions {
		if blofinPositions[i].Coin == coin {
			onChain = blofinPositions[i].Size
			found = true
			break
		}
	}
	if !found || onChain == 0 {
		return 0, false
	}
	absSzi := math.Abs(onChain)
	peers := blofinLiveStrategiesForCoin(coin, blofinLiveAll)
	if len(peers) <= 1 {
		return absSzi, true
	}
	weights := blofinStrategyCapitalWeights(peers)
	sumW := 0.0
	var wFiring float64
	foundFiring := false
	for i, p := range peers {
		sumW += weights[i]
		if p.ID == strategyID {
			wFiring = weights[i]
			foundFiring = true
		}
	}
	if !foundFiring || sumW <= 0 {
		return absSzi, true
	}
	q := absSzi * (wFiring / sumW)
	if q > absSzi {
		q = absSzi
	}
	if q < 1e-12 {
		return 0, false
	}
	return q, true
}

func runPendingBloFinCircuitCloses(
	ctx context.Context,
	state *AppState,
	strategies []StrategyConfig,
	blofinHasCreds bool,
	blofinPositions []BloFinPosition,
	blofinStateFetched bool,
	blofinFetcher BloFinPositionsFetcher,
	closer BloFinLiveCloser,
	totalBudget time.Duration,
	mu *sync.RWMutex,
	ownerDM func(string),
) {
	if !blofinHasCreds || closer == nil || state == nil {
		return
	}

	var blofinLiveAll []StrategyConfig
	for _, sc := range strategies {
		if sc.Platform == "blofin" && sc.Type == "perps" && blofinIsLive(sc.Args) {
			blofinLiveAll = append(blofinLiveAll, sc)
		}
	}

	mu.RLock()
	hasPending := false
	hasStuckCB := false
	for _, ss := range state.Strategies {
		if ss == nil {
			continue
		}
		if ss.RiskState.getPendingCircuitClose(PlatformPendingCloseBloFin) != nil {
			hasPending = true
		}
	}
	for _, sc := range blofinLiveAll {
		ss := state.Strategies[sc.ID]
		if ss == nil {
			continue
		}
		if ss.RiskState.getPendingCircuitClose(PlatformPendingCloseBloFin) == nil && ss.RiskState.CircuitBreaker {
			hasStuckCB = true
			break
		}
	}
	mu.RUnlock()

	if !hasPending && !hasStuckCB {
		return
	}

	ctxOverall, cancelOverall := context.WithTimeout(ctx, totalBudget)
	defer cancelOverall()

	positions := blofinPositions
	if !blofinStateFetched && blofinFetcher != nil {
		pos, err := blofinFetcher()
		if err != nil {
			fmt.Printf("[CRITICAL] blofin-circuit-close: cannot fetch BloFin positions: %v — will retry next cycle\n", err)
			return
		}
		positions = pos
	}

	if hasStuckCB {
		recoverOrder := make([]StrategyConfig, len(blofinLiveAll))
		copy(recoverOrder, blofinLiveAll)
		sort.Slice(recoverOrder, func(i, j int) bool { return recoverOrder[i].ID < recoverOrder[j].ID })
		mu.Lock()
		for _, sc := range recoverOrder {
			ss := state.Strategies[sc.ID]
			if ss == nil {
				continue
			}
			if ss.RiskState.getPendingCircuitClose(PlatformPendingCloseBloFin) != nil {
				continue
			}
			if !ss.RiskState.CircuitBreaker {
				continue
			}
			sym := blofinSymbol(sc.Args)
			if sym == "" {
				continue
			}
			qty, ok := computeBloFinCircuitCloseQty(sym, sc.ID, positions, blofinLiveAll)
			if !ok || qty <= 0 {
				continue
			}
			ss.RiskState.setPendingCircuitClose(PlatformPendingCloseBloFin, &PendingCircuitClose{
				Symbols: []PendingCircuitCloseSymbol{{Symbol: sym, Size: qty}},
			})
			fmt.Printf("[CRITICAL] blofin-circuit-close: recovered pending for strategy %s coin %s sz=%.6f (CB latched, BloFin fetch had failed at fire time)\n",
				sc.ID, sym, qty)
		}
		mu.Unlock()
	}

	type job struct {
		stratID string
		pending PendingCircuitClose
	}
	var jobs []job
	mu.RLock()
	for id, ss := range state.Strategies {
		if ss == nil {
			continue
		}
		p := ss.RiskState.getPendingCircuitClose(PlatformPendingCloseBloFin)
		if p == nil || len(p.Symbols) == 0 {
			continue
		}
		jobs = append(jobs, job{id, *p})
	}
	mu.RUnlock()

	if len(jobs) == 0 {
		return
	}

	sort.Slice(jobs, func(i, j int) bool { return jobs[i].stratID < jobs[j].stratID })

	for _, j := range jobs {
		if err := ctxOverall.Err(); err != nil {
			fmt.Printf("[CRITICAL] blofin-circuit-close: budget exhausted: %v\n", err)
			return
		}
		sc := lookupStrategyConfig(strategies, j.stratID)
		if sc == nil || sc.Platform != "blofin" || sc.Type != "perps" || !blofinIsLive(sc.Args) {
			mu.Lock()
			if ss := state.Strategies[j.stratID]; ss != nil {
				ss.RiskState.clearPendingCircuitClose(PlatformPendingCloseBloFin)
			}
			mu.Unlock()
			continue
		}

		allOK := true
		var failedSym string
		var failedSz float64
		var failedErr error
		for _, c := range j.pending.Symbols {
			if err := ctxOverall.Err(); err != nil {
				allOK = false
				break
			}
			sz := c.Size
			for _, p := range positions {
				if p.Coin != c.Symbol {
					continue
				}
				absOC := math.Abs(p.Size)
				if absOC <= 1e-15 {
					sz = 0
					break
				}
				if sz > absOC {
					sz = absOC
				}
				break
			}
			if sz <= 1e-15 {
				continue
			}
			partial := sz
			_, err := closer(c.Symbol, &partial)
			if err != nil {
				fmt.Printf("[CRITICAL] blofin-circuit-close: strategy %s coin %s sz=%.6f failed: %v\n", j.stratID, c.Symbol, sz, err)
				allOK = false
				failedSym = c.Symbol
				failedSz = sz
				failedErr = err
				break
			}
			fmt.Printf("[INFO] blofin-circuit-close: strategy %s coin %s submitted reduce-only close sz=%.6f\n", j.stratID, c.Symbol, sz)
		}

		var failCount int
		var shouldAlert bool
		now := time.Now().UTC()
		mu.Lock()
		if ss := state.Strategies[j.stratID]; ss != nil {
			if allOK {
				ss.RiskState.clearPendingCircuitClose(PlatformPendingCloseBloFin)
			} else if failedErr != nil {
				if p := ss.RiskState.getPendingCircuitClose(PlatformPendingCloseBloFin); p != nil {
					p.ConsecutiveFailures++
					failCount = p.ConsecutiveFailures
					if shouldNotifyDrainFailure(p.ConsecutiveFailures, p.LastNotifiedAt, now) {
						p.LastNotifiedAt = now
						shouldAlert = true
					}
				}
			}
		}
		mu.Unlock()

		if shouldAlert && ownerDM != nil && failedErr != nil {
			ownerDM(formatDrainFailureAlert("blofin", j.stratID, failedSym, failedSz, failedErr.Error(), failCount))
		}
	}
}
