package main

import "fmt"

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

// runBloFinCheck runs check_blofin.py signal-check mode (Phase 3, no lock).
func runBloFinCheck(sc StrategyConfig, prices map[string]float64, posCtx PositionCtx, regime *RegimeConfig, notifier *MultiNotifier, logger *StrategyLogger) (*BloFinResult, string, float64, bool) {
	args := append([]string{}, sc.Args...)
	args = appendOpenCloseArgs(args, sc, posCtx)
	if sc.HTFFilter {
		args = append(args, "--htf-filter")
	}
	args = appendRegimeArgs(args, regime)
	args = appendStrategyRegimeWindowArgs(args, sc, regime)
	args = appendRegimePayloadArg(args, sc, regime)
	if refsArgs, err := buildStrategyRefsArg(sc); err != nil {
		logger.Warn("Failed to marshal strategy refs: %v", err)
	} else if len(refsArgs) > 0 {
		args = append(args, refsArgs...)
	}
	if sym := blofinSymbol(sc.Args); sym != "" {
		if mid, ok := prices[sym]; ok && mid > 0 {
			args = append(args, fmt.Sprintf("--mark-price=%g", mid))
		}
	}
	logger.Info("Running: python3 %s %v", sc.Script, args)

	result, stderr, err := RunBloFinCheck(sc.Script, args)
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

// runBloFinExecuteOrder places a live BloFin order (Phase 3, no lock).
func runBloFinExecuteOrder(sc StrategyConfig, result *BloFinResult, price, cash, posQty float64, posSide string, avgCost float64, notifier *MultiNotifier, logger *StrategyLogger) (*BloFinExecuteResult, bool) {
	signal := result.Signal
	side := "buy"
	if signal < 0 {
		side = "sell"
	}
	effectiveDir := EffectiveDirection(sc)
	if effectiveDir == DirectionLong && signal < 0 {
		logger.Info("BloFin: direction=long, skipping sell signal")
		return nil, true
	}
	if effectiveDir == DirectionShort && signal > 0 {
		logger.Info("BloFin: direction=short, skipping buy signal")
		return nil, true
	}
	sym := result.Symbol
	notional := ComputePerpsOpenNotional(sc, cash)
	if notional <= 0 {
		logger.Info("BloFin: notional <= 0, skipping order")
		return nil, false
	}
	size := ComputePerpsSize(sc, notional, price)
	if size <= 0 {
		logger.Info("BloFin: computed size <= 0, skipping order")
		return nil, false
	}
	if result.StopLossPrice > 0 {
		logger.Info("BloFin: SL price=%.2f for %s", result.StopLossPrice, sym)
	}
	logger.Info("BloFin: placing %s order %s sz=%.6f (notional=%.2f)", side, sym, size, notional)
	execResult, stderr, err := RunBloFinExecute(sc.Script, sym, side, size, result.StopLossPrice)
	if stderr != "" {
		logger.Warn("BloFin execute stderr: %s", stderr)
	}
	if err != nil {
		logger.Error("BloFin execute failed: %v", err)
		notifyScriptFailure(notifier, sc, scriptFailureError, fmt.Sprintf("live execute failed for %s: %v", sym, err))
		return nil, false
	}
	if execResult.Error != "" {
		logger.Error("BloFin execute error: %s", execResult.Error)
		notifyScriptFailure(notifier, sc, scriptFailureError, fmt.Sprintf("live execute error for %s: %s", sym, execResult.Error))
		return nil, false
	}
	return execResult, true
}

// executeBloFinResult applies a BloFin result to state. Must be called under Lock.
func executeBloFinResult(sc StrategyConfig, s *StrategyState, db *StateDB, result *BloFinResult, execResult *BloFinExecuteResult, signalStr string, price float64, regime *RegimeConfig, logger *StrategyLogger) (int, string) {
	sym := result.Symbol
	detail := ""
	trades := 0

	fillPrice := price
	var fillQty float64
	var fillFee float64
	var fillOID string
	if execResult != nil && execResult.Execution != nil && execResult.Execution.Fill != nil && execResult.Execution.Fill.AvgPx > 0 {
		fillPrice = execResult.Execution.Fill.AvgPx
		fillQty = execResult.Execution.Fill.TotalSz
		fillFee = execResult.Execution.Fill.Fee
		fillOID = execResult.Execution.Fill.OID
		logger.Info("Live fill at $%.2f qty=%.6f (mid was $%.2f)", fillPrice, fillQty, price)
	}

	if result.StopLossPrice > 0 {
		logger.Info("SL hit for %s: sl_price=$%.2f atr_value=%.2f", result.Symbol, result.StopLossPrice, result.ATRValue)
	}

	exec, err := ExecutePerpsSignalWithLeverageDeferredOpen(s, result.Signal, result.Symbol, fillPrice, PerpsSizingFor(sc, fillPrice, result.ATRValue), fillQty, fillOID, fillFee, EffectiveDirection(sc), result.CloseFraction, logger)
	if err != nil {
		logger.Error("Trade execution failed: %v", err)
		return 0, ""
	}
	trades = exec.TradesExecuted
	stampEntryATRIfOpened(s, result.Symbol, result.Indicators)
	stampPositionRegimeIfOpened(s, result.Symbol, regimePayloadValue(result.Regime), sc, regime)
	if pos, ok := s.Positions[sym]; ok {
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
