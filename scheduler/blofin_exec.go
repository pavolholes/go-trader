package main

import (
	"fmt"
	"strconv"
	"strings"
)

// blofinIsLive reports whether --mode=live appears in strategy args.
func blofinIsLive(args []string) bool {
	return isLiveArgs(args)
}

// blofinSymbol extracts the symbol from BloFin strategy args (e.g. "BTC").
func blofinSymbol(args []string) string {
	if len(args) >= 2 {
		return args[1]
	}
	return ""
}

func applyBloFinManageOnly(result *BloFinResult, posQty float64, posSide string) {
	if result == nil {
		return
	}
	if result.CloseFraction > 0 && posQty > 0 {
		result.Signal = composeOpenCloseSignal("none", result.CloseFraction, posSide)
		return
	}
	result.Signal = 0
}

// runBloFinCheck runs check_blofin.py signal-check mode (Phase 3, no lock).
var runBloFinCheckFn = RunBloFinCheck

func runBloFinCheck(sc StrategyConfig, prices map[string]float64, posCtx PositionCtx, regime *RegimeConfig, notifier *MultiNotifier, logger *StrategyLogger) (*BloFinResult, string, float64, bool) {
	args := append([]string{}, sc.Args...)
	args = appendOpenCloseArgs(args, sc, posCtx)
	args = appendBloFinPositionContextArgs(args, posCtx)
	if sc.HTFFilter {
		args = append(args, "--htf-filter")
	}
	args = appendRegimeArgs(args, regime)
	args = appendStrategyRegimeWindowArgs(args, sc, regime)
	args = appendRegimePayloadArg(args, sc, regime)
	if sc.Platform == "blofin" && sc.Type == "perps" && posCtx.Quantity > 0 && posCtx.BarsHeldKnown && blofinCloseNeedsPositionHistory(sc) {
		limit := posCtx.BarsHeld + 2
		if limit < 200 {
			limit = 200
		}
		if limit > 5000 {
			limit = 5000
		}
		args = append(args, "--ohlcv-limit", strconv.Itoa(limit))
	}
	if refsArgs, err := buildStrategyRefsArg(sc, "", false); err != nil {
		logger.Warn("Failed to marshal strategy refs: %v", err)
	} else if len(refsArgs) > 0 {
		args = append(args, refsArgs...)
	}
	if sym := blofinSymbol(sc.Args); sym != "" {
		if mid, ok := prices[sym]; ok && mid > 0 {
			args = append(args, fmt.Sprintf("--mark-price=%g", mid))
		}
	}
	args = appendAllowNoEdgeArg(args, sc)
	logger.Info("Running: python3 %s %v", sc.Script, args)

	result, stderr, err := runBloFinCheckFn(sc.Script, args)
	if err != nil {
		logger.Error("Script failed: %v", err)
		if stderr != "" {
			logger.Error("stderr: %s", stderr)
		}
		notifyScriptFailure(notifier, sc, scriptFailureCrash, err.Error())
		return nil, "", 0, false
	}
	if stderr != "" {
		logger.Info("stderr: %s", stderr)
	}
	for _, warning := range result.CloseContextWarnings {
		logger.Warn("BloFin close context: %s", warning)
	}
	if result.Error != "" {
		logger.Error("Script returned error: %s", result.Error)
		notifyScriptFailure(notifier, sc, scriptFailureError, result.Error)
		return nil, "", 0, false
	}
	clearScriptFailure(notifier, sc)

	signalStr := signalLabel(result.Signal)
	logger.Info("Signal: %s | %s @ $%.2f [%s]", signalStr, result.Symbol, result.Price, result.Mode)

	price := result.Price
	if price <= 0 {
		if p, ok := prices[result.Symbol]; ok {
			price = p
		}
	}
	if price <= 0 {
		logger.Error("No price available for %s", result.Symbol)
		return nil, "", 0, false
	}
	return result, signalStr, price, true
}

func blofinCloseNeedsPositionHistory(sc StrategyConfig) bool {
	for _, ref := range sc.closeRefs() {
		name := strings.ToLower(strings.TrimSpace(ref.Name))
		if name == "avwap_stop" || name == "time_stop" {
			return true
		}
	}
	return false
}

func appendBloFinPositionContextArgs(args []string, pos PositionCtx) []string {
	if pos.Quantity <= 0 {
		return args
	}
	out := append([]string(nil), args...)
	if !pos.OpenedAt.IsZero() {
		out = append(out, "--position-opened-at-ms", strconv.FormatInt(pos.OpenedAt.UTC().UnixMilli(), 10))
	}
	if pos.BarsHeldKnown {
		out = append(out, "--position-bars-held", strconv.Itoa(pos.BarsHeld))
	}
	if label := strings.TrimSpace(pos.RegimeAppliedLabel); label != "" {
		out = append(out, "--position-regime-applied", label)
	}
	if label := strings.TrimSpace(pos.RegimePendingLabel); label != "" {
		out = append(out, "--position-regime-pending-label", label)
		out = append(out, "--position-regime-pending-count", strconv.Itoa(pos.RegimePendingCount))
	}
	return out
}

// runBloFinExecuteOrder places a live BloFin order (Phase 3, no lock).
func runBloFinExecuteOrder(sc StrategyConfig, result *BloFinResult, price, cash, posQty float64, posSide string, avgCost float64, notifier *MultiNotifier, logger *StrategyLogger) (*BloFinExecuteResult, bool) {
	signal := result.Signal
	isClose := result.CloseFraction > 0 && posQty > 0
	// SELL/close signál bez otvorenej pozície nemá čo zatvárať — tichý noop.
	// (Len pre long-only; pri both je SELL bez pozície legitímny short open.)
	if posQty <= 0 && (signal < 0 || result.CloseFraction > 0) && EffectiveDirection(sc) == DirectionLong {
		logger.Info("BloFin: sell/close signal with no open position (long-only), skipping")
		return nil, true
	}
	side := "buy"
	if isClose {
		// Close: opposite of position side
		if posSide == "long" {
			side = "sell"
		} else if posSide == "short" {
			side = "buy"
		} else if signal < 0 {
			side = "sell"
		}
	} else if signal < 0 {
		side = "sell"
	}
	effectiveDir := EffectiveDirection(sc)
	if effectiveDir == DirectionLong && signal < 0 && !isClose {
		logger.Info("BloFin: direction=long, skipping sell signal")
		return nil, false
	}
	if effectiveDir == DirectionShort && signal > 0 {
		logger.Info("BloFin: direction=short, skipping buy signal")
		return nil, false
	}
	sym := result.Symbol
	notional := ComputePerpsOpenNotional(sc, cash)
	if notional <= 0 {
		logger.Info("BloFin: notional <= 0, skipping order")
		return nil, false
	}
	size := ComputePerpsSize(sc, notional, price)
	// Close signaly: burza musi dostat rovnaku velkost ako DB (posQty x closeFraction),
	// inak sa partial close vykona ako full close a stav sa rozide.
	if result.CloseFraction > 0 && posQty > 0 {
		if result.CloseFraction < 1 {
			size = posQty * result.CloseFraction
		} else {
			size = posQty
		}
	}
	if size <= 0 {
		logger.Info("BloFin: computed size <= 0, skipping order")
		return nil, false
	}
	if result.StopLossPrice > 0 {
		logger.Info("BloFin: SL price=%.2f for %s", result.StopLossPrice, sym)
	}
	logger.Info("BloFin: placing %s order %s sz=%.6f price=%.6f (notional=%.2f) posSide=%s closeFrac=%.4f", side, sym, size, price, notional, posSide, result.CloseFraction)
	execResult, stderr, err := RunBloFinExecute(sc.Script, sym, side, size, result.StopLossPrice, isClose, posSide, isClose, sc.Leverage)
	if stderr != "" {
		logger.Warn("BloFin execute stderr: %s", stderr)
	}
	if err != nil {
		logger.Error("BloFin execute failed: %v", err)
		notifyLiveExecuteFailure(notifier, sc, fmt.Sprintf("live execute failed for %s: %v", sym, err))
		return nil, false
	}
	if execResult.Error != "" {
		logger.Error("BloFin execute error: %s", execResult.Error)
		notifyLiveExecuteFailure(notifier, sc, fmt.Sprintf("live execute error for %s: %s", sym, execResult.Error))
		return nil, false
	}
	if execResult.Skipped != "" {
		logger.Info("BloFin execute skipped for %s: %s", sym, execResult.Skipped)
		return nil, false
	}
	return execResult, true
}

func blofinCloseAttribution(result *BloFinResult) *CloseAttribution {
	if result == nil {
		return nil
	}
	source := strings.TrimSpace(result.CloseSource)
	if source == "" {
		if result.CloseFraction > 0 {
			source = "evaluator"
		} else {
			source = "signal"
		}
	}
	evaluator := strings.TrimSpace(result.CloseEvaluator)
	if evaluator == "" && source == "evaluator" {
		evaluator = strings.TrimSpace(result.CloseStrategy)
	}
	tier := ""
	if result.TPTier != nil {
		tier = fmt.Sprint(result.TPTier)
	}
	return &CloseAttribution{
		Source:            source,
		Evaluator:         evaluator,
		Reason:            strings.TrimSpace(result.CloseReason),
		TPTier:            tier,
		StopLossTriggerPx: result.StopLossPrice,
	}
}

// executeBloFinResult applies a BloFin result to state. Must be called under Lock.
func executeBloFinResult(sc StrategyConfig, s *StrategyState, db *StateDB, result *BloFinResult, execResult *BloFinExecuteResult, signalStr string, price float64, regime *RegimeConfig, logger *StrategyLogger, notifier *MultiNotifier) (int, string) {
	sym := result.Symbol
	detail := ""
	trades := 0

	fillPrice := price
	var fillQty float64
	var fillFee float64
	var fillOID string
	var fillClientOrderID string
	if execResult != nil && execResult.Execution != nil && execResult.Execution.Fill != nil {
		fillOID = execResult.Execution.Fill.OID
		fillClientOrderID = execResult.Execution.Fill.ClientOrderID
		if execResult.Execution.Fill.AvgPx > 0 {
			fillPrice = execResult.Execution.Fill.AvgPx
			fillQty = execResult.Execution.Fill.TotalSz
			fillFee = execResult.Execution.Fill.Fee
			logger.Info("Live fill at $%.2f qty=%.6f (mid was $%.2f)", fillPrice, fillQty, price)
		}
	}

	if result.StopLossPrice > 0 {
		logger.Info("SL hit for %s: sl_price=$%.2f atr_value=%.2f", result.Symbol, result.StopLossPrice, result.ATRValue)
	}

	// Live: bez burzoveho fillu sa nic nezapisuje do DB (ziadne fantomy).
	// Paper fill=mark cena je OK len pre paper (execResult==nil).
	if execResult != nil && fillQty <= 0 {
		if fillOID != "" || fillClientOrderID != "" {
			logger.Warn("BloFin order has no confirmed fill yet for %s (order=%s client=%s) — skipping DB write; Copy sync will reconcile", sym, fillOID, fillClientOrderID)
			notifyBloFinOrderFillPending(notifier, sc, signalStr, sym, fillOID, fillClientOrderID)
		} else {
			logger.Error("BloFin live order without a fill reference for %s — skipping DB write", sym)
			notifyLiveExecuteFailure(notifier, sc, fmt.Sprintf("live order without fill reference for %s", sym))
		}
		return 0, ""
	}
	exec, err := ExecutePerpsSignalWithLeverageDeferredOpenAttributed(s, result.Signal, result.Symbol, fillPrice, PerpsSizingFor(sc, fillPrice, result.ATRValue), fillQty, fillOID, fillFee, EffectiveDirection(sc), result.CloseFraction, logger, blofinCloseAttribution(result))
	if err != nil {
		logger.Error("Trade execution failed: %v", err)
		return 0, ""
	}
	trades = exec.TradesExecuted
	stampEntryATRIfOpened(s, result.Symbol, result.Indicators)
	stampPositionRegimeIfOpened(s, result.Symbol, regimePayloadValue(result.Regime), sc, regime)
	if pos, ok := s.Positions[sym]; ok {
		armBloFinVirtualStopOnOpen(sc, pos)
		if sc.Platform == "blofin" && execResult != nil && execResult.Execution != nil && execResult.Execution.Fill != nil && execResult.Execution.Fill.ContractValue > 0 {
			pos.Multiplier = execResult.Execution.Fill.ContractValue
		}
		recordPositionOpen(s, sc, exec.OpenTrade, pos)
	}

	detail = ""
	if trades > 0 {
		prefix := ""
		if execResult != nil {
			prefix = "LIVE "
		}
		detail = fmt.Sprintf("[%s] %s%s %s @ $%.2f", sc.ID, prefix, signalStr, result.Symbol, fillPrice)
	}
	return trades, detail
}

func notifyBloFinOrderFillPending(notifier *MultiNotifier, sc StrategyConfig, signal, symbol, orderID, clientOrderID string) {
	if notifier == nil || !notifier.HasBackends() {
		return
	}
	refs := "order reference unavailable"
	if orderID != "" {
		refs = "order=" + orderID
	}
	if clientOrderID != "" {
		if refs == "order reference unavailable" {
			refs = ""
		} else {
			refs += " "
		}
		refs += "client=" + clientOrderID
	}
	content := fmt.Sprintf("⏳ **BLOFIN ORDER AWAITING FILL** [%s] %s %s (%s)\nBloFin has not confirmed a fill; Go-Trader has not recorded a trade. This is not a rejection—the order may still be pending. Check the OID/client ID in BloFin history before retrying to avoid a duplicate; confirmed fills will be reconciled automatically.", sc.ID, signal, symbol, refs)
	notifier.SendToAllChannels(content)
}
