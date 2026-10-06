# BloFin Close-Evaluator Support Audit

**Snapshot:** 2026-10-03  
**Purpose:** implementation handoff for a follow-up coding agent  
**Status:** audit only; no Go code, live configuration, or trading behavior was changed.

## Objective and guardrails

Make the close evaluators that are advertised/registered behave correctly end-to-end on **BloFin perpetuals**: configuration validation, live context, evaluation, close sizing, risk protection, persisted state, diagnostics, and backtest parity. Do not change the 11 live strategy configurations as part of this implementation task. After the BloFin support and parity work is complete, rerun the strategy-by-strategy evaluator comparison described at the end of this document.

The Go configuration is intentionally a **single `close_strategy` per strategy** model. Do not restore the old array/stack model as a shortcut: `scheduler/config.go` collapses legacy `close_strategies` to one entry and says to keep one profit-taking close while moving independent risk backstops to the strategy level.

## Deployment and configuration snapshot

Three Go-trader containers were present:

| Container | Role/config | Data note |
|---|---|---|
| `go-trader-demo` | Demo config v16; 1,345 configured strategies | Main `state.db` contained 72,014 closed positions, 2026-06-28 through 2026-10-02. |
| `go-trader-live` | Live config v16; 11 strategies | Its own state DB contained 81 closed positions, 2026-09-23 through 2026-10-03, plus two open positions at query time. |
| `go-trader-ob-touch` | Separate `config-ob-touch.json`; 92 strategies | Shares the demo data volume but uses `ob_touch-state.db`; it was not included in the main demo-history ranking. |

All 11 current live strategies use `tiered_tp_atr_regime`, `use_defaults=true`, `sl_atr_mult=2.5`. Their matching demo strategies used the same evaluator with `sl_atr_mult=1.5`; demo capital was $1,000 per strategy, while live capital is $100. Demo `portfolio_risk.max_drawdown_pct` was 100 with per-strategy `max_drawdown_pct=50`; live global portfolio drawdown is 20. Demo and live both use composite regime windows (short 7 / medium 14 / long 30), but four entry configurations differ:

- `consolidation_range-XLM-15m`: demo `direction=both` plus regime directional policy; live `both` without the policy.
- `session_breakout-ADA-30m`: demo `both` plus policy; live `long` without the policy.
- `regime_adaptive-SPX-15m`: demo `both` plus policy; live `long` without the policy.
- `liquidity_sweeps-ETH-30m`: demo `both` plus policy; live `long` without the policy.

Other demo/live leverage differences also exist (notably ETH, SPCX, and USELESS). Consequently, demo PnL is evidence about each historical combination, not an apples-to-apples measurement of the current live configuration.

## Verified BloFin close-evaluation path

The current direct-check path is:

1. `scheduler/blofin_exec.go::runBloFinCheck` calls `appendOpenCloseArgs` and `buildStrategyRefsArg` from `scheduler/strategy_composition.go`; the close ref and its params are serialized into `--strategy-refs`.
2. `shared_scripts/check_blofin.py::run_signal_check` creates a market context with mark price, latest ATR, and the current live regime, then calls `shared_tools/strategy_composition.evaluate_open_close` using `close_registry_loader.evaluate`.
3. `shared_tools/strategy_composition.finalize_decision` returns the winning close fraction, evaluator name, optional SL price, ATR value, and optional tier fill price.
4. `scheduler/blofin_exec.go::runBloFinExecuteOrder` sizes a close as current position quantity × close fraction and executes the opposite-side close. This is a live-relevant path; the close evaluator is not merely a backtester/UI feature.

The passed BloFin position context includes side, average cost, current and initial quantity, entry ATR, risk anchor, and position regime. The check path does **not** currently pass `bars_held` or `zscore` in the context. `evaluate_open_close` injects AVWAP only if the open strategy result contains an `avwap` column. The default `check_blofin.py` market context does not calculate a z-score.

The live tiered evaluator defaults `tp_enabled` to true when the parameter is absent. In contrast, `backtest/run_backtest.py::run_single_backtest` inserts `tp_enabled=false` when it is absent. Local comparison scripts such as `paper_vs_backtest.py` and `close_reasons_comparison.py` pass a tiered close config without explicit `tp_enabled=true`; those calls therefore do not reproduce the live TP behavior. Any parity test must make the TP setting explicit.

## Registered evaluator inventory versus BloFin readiness

The Python registry in `shared_strategies/close/registry.py` contains exactly 12 names. Registry presence alone does not mean that Go configuration validation and BloFin runtime context support the evaluator.

| Evaluator | BloFin observations / gap |
|---|---|
| `atr_stop` | Stop-only full close. Can use entry or live ATR; does not take profit. |
| `avwap_stop` | Stop-only. Requires `market.avwap`; the check path only injects it when the open strategy output contains `avwap`. Validate actual availability per strategy. |
| `tiered_tp_atr` | Fixed ATR tiers. Python implementation checks `sl_atr_mult` before TP, so this is the closest safe competing family to the current evaluator. |
| `tiered_tp_atr_live` | Fixed tier ladder measured using live ATR. The evaluator does not implement the current regime evaluator’s integrated `sl_atr_mult` behavior. Do not switch without a verified independent BloFin stop path. |
| `tiered_tp_atr_live_regime` | Live ATR/current regime TP. The Python evaluator is profit-taking only; verify how a protective SL is retained on BloFin before enabling. |
| `tiered_tp_atr_live_regime_dynamic` | Go validation in `scheduler/regime_atr.go` restricts this to Hyperliquid perps/manual. The dynamic-regime stop/rearm path in `scheduler/regime_dynamic_tp_sl.go` is also Hyperliquid-specific. Not currently a BloFin config option. |
| `tiered_tp_atr_regime` | Current live evaluator. Resolves TP tiers using the position’s regime and entry ATR; checks `sl_atr_mult` before TP. Built-in default ladders are clean trend 2.5/4/5.5/7 ATR, choppy 1.5/3/5 ATR, and ranging 0.5/1 ATR. |
| `tiered_tp_pct` | Fixed percent TP ladder. No ATR stop in the evaluator. A switch must not silently remove current stop protection. |
| `time_stop` | Requires `position.bars_held`; current BloFin `PositionCtx`/CLI args do not provide it, so this currently no-ops for live BloFin. |
| `trailing_tp_ratchet` | Go config validation in `scheduler/trailing_tp_ratchet.go` restricts `trailing_tp_ratchet*` to Hyperliquid perps/manual. The scheduler ratchet state/SL integration is not currently enabled for BloFin. |
| `trailing_tp_ratchet_regime` | Same Hyperliquid-only restriction; additionally needs `trailing_stop_atr_mult_regime`. Do not treat a successful Python unit test/backtest as proof of BloFin live support. |
| `zscore_target` | Requires `market.zscore`; current BloFin check context does not provide it, so it currently no-ops. |

The Go-side restrictions are stricter than the registry’s broad platform catalogue. Keep the registry, config validator, UI/Discord catalogue, Python check path, and scheduler implementation aligned. Unsupported combinations should fail clearly at config load, not silently return a no-op.

## Implementation work for the follow-up agent

### P0 — Define and enforce one platform support contract

- Create/verify a single explicit support matrix for evaluator × platform × strategy type × required context.
- Reconcile `close_registry_loader`/registry catalogue entries with Go restrictions (especially ratchet and dynamic regime close). If BloFin support is not implemented, mark it unsupported consistently in catalogue/UI and keep config rejection explicit.
- Preserve the one-close-evaluator-per-strategy model. Do not accept an array whose extra evaluators are silently ignored.

### P1 — Make risk protection survive evaluator changes

- The current BloFin baseline gets TP and `sl_atr_mult` from `tiered_tp_atr_regime`. Several alternatives in the registry are TP-only or stop-only. A config change must not silently remove the current SL.
- Document which BloFin surface owns the hard/ATR stop when the selected TP evaluator does not return one. If there is no independent BloFin stop owner, reject that configuration or implement the separate risk path before exposing it as supported.
- Keep long/short handling, partial-close sizing, fee accounting, and reduce-only behavior consistent. Validate `close_fraction=1` and partial fractions against current remaining quantity.

### P2 — Implement or explicitly defer BloFin trailing-ratchet support

- `scheduler/trailing_tp_ratchet.go` already has ratchet tier resolution, a high-water mark, `PostTPTrailingATRMult`, and tighter-trail state, but configuration currently rejects the evaluator for non-Hyperliquid platforms.
- Audit every call site of `applyTrailingTPRatchet` and the BloFin check/execute lifecycle. A BloFin implementation must persist tier progress/high-water/trail state across scheduler cycles and restarts, emit a correctly sized close on a breached trail, and retain an initial protective stop. Do not rely on Hyperliquid resting-order behavior for BloFin.
- If this work is out of scope, keep both ratchet evaluator names clearly unavailable for BloFin; do not recommend them as live candidates.

### P3 — Implement or explicitly defer dynamic regime close on BloFin

- `tiered_tp_atr_live_regime_dynamic` is currently rejected for non-HL platforms. Decide whether to keep that boundary or add an actual BloFin implementation.
- The Go unified config supports per-regime `tp_tiers` and `stop_loss_atr`; verify that the BloFin Python close evaluation consumes both. The Python live-regime evaluator currently evaluates tiered TP from the live regime/ATR but does not itself implement the unified per-regime `stop_loss_atr` stop.
- If supported, confirm the two-cycle regime confirmation contract, state ownership, stale/pending regime behavior, and whether a regime switch tightens or loosens an already armed stop. Test adverse moves during a pending regime transition.

### P4 — Supply the context-dependent evaluators’ inputs

- `time_stop`: add a reliable position open timestamp or completed-bar counter to `PositionCtx` and the BloFin check CLI. Derive bars from the strategy timeframe, count only completed bars, and test restart/reconnect behavior.
- `zscore_target`: define the lookback and closed-candle z-score calculation and include it in the BloFin market context without look-ahead. Test long (`z >= target`) and short (`z <= -target`) behavior, missing warmup data, and NaN handling.
- `avwap_stop`: verify which open strategies emit the required `avwap`; add a clear config-time requirement or a canonical AVWAP provider. A missing AVWAP must be visible as unsupported/missing context, not a silent assumed stop.

### P5 — Persist evaluator attribution and diagnostics

- Current live `closed_positions.close_reason` is generic (`close` vs `signal`). In the sampled live DB, `trade_diagnostics.close_reason` did not align one-to-one with the generic `close` category. Persist evaluator name/reason, TP tier, SL trigger, and whether the close was evaluator-, signal-, or circuit-breaker-driven.
- Ensure MFE/MAE/capture diagnostics are attached to evaluator-triggered closes and account for partial TP realized PnL. The current `capture_ratio` is price-only, capped at 1, and only written for profitable exits; losses have NULL. Preserve that distinction in reports or add a separately named partial-fill-aware metric.

### P6 — Repair the backtest/live parity before ranking evaluators

- Explicitly mirror the live TP setting. `run_single_backtest` currently defaults missing `tp_enabled` to false; tests for the live BloFin tiered path must specify/resolve the live true behavior.
- `shared_tools/data_fetcher.load_cached_data` fetches only when the cache is empty. The observed cache had nonempty but stale rows ending around 2026-06-18; `load_cached_data` did not refresh the missing tail. Add a cache coverage/end-date refresh rule or fail with a clear stale-range message.
- Make backtests use the exact config snapshot per test window: direction, regime policy, composite windows, close params, sizing, and the correct risk/circuit-breaker model. Keep the global portfolio circuit breaker separate from the backtester’s margin-based per-trade circuit breaker.
- Reproduce signal/reversal exits and close reasons before interpreting evaluator-return deltas. One XLM replay had 34 trade legs and 20 CB/14 TP closes at one setting versus 229 paper positions with 105 signal/104 SL/20 CB; it is not a valid evaluator ranking yet.

## Acceptance criteria

1. A BloFin config support test covers every evaluator and rejects unsupported platform/context combinations clearly.
2. Paper/fake-adapter integration tests drive a BloFin check through `strategy_refs` and verify the result reaches `CloseFraction`, `StopLossPrice`, evaluator name/reason, and actual close quantity.
3. Long and short tests cover missing context, each TP tier, repeated checks after partial closes, exact full close, ATR stop precedence, and evaluator/signal conflict precedence.
4. Ratchet tests (if enabled for BloFin) verify tier progression, high-water mark, trail tightening only, persistence/restart, and correct market-close sizing without assuming a resting Hyperliquid order.
5. `time_stop`, `zscore_target`, and AVWAP tests use real live-context fields; a configured evaluator may not silently remain a no-op because the context was never wired.
6. A parity test confirms the same candle/position/config produces compatible close decision/reason between `check_blofin.py` and the backtester; partial TP, SL, fees, and signal reversals are included.
7. `save=False` and store-free data tests make no cache/result DB writes; stale nonempty cache is detected.
8. Existing Go/Python suites pass; do not edit the live config as part of this implementation brief.

## Baseline for the later 11-strategy reassessment

All values below are demo realized PnL from a $1,000 strategy allocation; the live sample is a separate, shorter period. `cap` is the mean positive-winner-only diagnostic capture ratio described above.

| Strategy | Demo N / PnL / win% / PF / cap | Live N / PnL; `close` / `signal` PnL |
|---|---:|---:|
| `consolidation_range-XLM-15m` | 229 / +$436.19 / 37.6 / 1.80 / .475 | 10 / +$17.75; +$8.04 / +$9.71 |
| `sma_crossover-ETH-30m` | 27 / +$171.29 / 55.6 / 2.86 / .867 | 4 / −$11.31; +$13.01 / −$24.33 |
| `stoch_rsi-DOGE-1h` | 54 / +$165.22 / 31.5 / 1.77 / .387 | 8 / +$76.09; +$40.44 / +$35.65 |
| `heikin_ashi_ema-ETH-30m` | 57 / +$160.85 / 42.1 / 1.56 / .780 | 9 / −$3.57; +$8.52 / −$12.09 |
| `order_blocks-SPCX-15m` | 28 / +$133.72 / 71.4 / 9.22 / .791 | 12 / +$17.04; +$0.48 / +$16.56 |
| `liquidity_sweeps-ETH-30m` | 20 / +$131.72 / 55.0 / 2.77 / .800 | 2 / +$3.38; +$2.40 / +$0.98 |
| `session_breakout-ADA-30m` | 30 / +$97.47 / 50.0 / 1.62 / .602 | 4 / −$21.55; +$24.63 / −$46.18 |
| `regime_adaptive-SPX-15m` | 43 / +$96.43 / 55.8 / 1.32 / .783 | 7 / +$7.44; +$30.49 / −$23.06 |
| `heikin_ashi_ema-HYPE-30m` | 54 / +$74.60 / 40.7 / 1.39 / .888 | 9 / +$13.40; +$7.08 / +$6.32 |
| `parabolic_sar-ETH-15m` | 30 / +$67.50 / 50.0 / 1.83 / .744 | 9 / +$4.71; +$5.07 / −$0.36 |
| `ema_crossover-USELESS-15m` | 39 / +$58.40 / 43.6 / 2.19 / .504 | 7 / +$4.80; +$3.74 / +$1.05 |

For demo, prominent CB contributions were XLM −$477.96 (20 positions), HA-ETH −$275.84 (30), SPX regime-adaptive −$299.48 (16), Stoch RSI −$203.45 (3), and HYPE −$182.30 (30). Many evaluator-independent signal closes were profitable. In the sampled live DB there were 81 closes total; generic `close` rows summed about +$143.90 over 22 positions and `signal` rows −$35.75 over 59. These live numbers are short-sample evidence, not a stable ranking.

## Follow-up strategy test matrix (after BloFin support/parity is fixed)

1. Re-run all 11 using exact current-live and demo configs as separate cohorts; never pool their PnL.
2. For each, keep `tiered_tp_atr_regime` as control and compare an SL-preserving fixed `tiered_tp_atr` challenger. Independently test `sl_atr_mult=1.5` versus 2.5; do not confound evaluator name and stop distance in one comparison.
3. Highest evaluator A/B priority: XLM consolidation, DOGE Stoch RSI, USELESS EMA, then ETH liquidity sweeps. Check session breakout/SPX/SMA/HA signal exits separately before attributing their losses to the close evaluator.
4. Use a chronological train/holdout split, same fees/size/direction/regime and full closed-bar feed. Report per-strategy net PnL, PF, max drawdown, CB and signal contribution, time in trade, and winner capture; reject improvements that only appear in the optimization window or worsen tail loss.
5. Only after those results, reassess the 11 live close refs individually. Do not bulk-change all live strategies based on the demo table or this implementation audit.

## Results-review handoff (2026-10-04)

The strategy-by-strategy reassessment and the additional SPCX/USELESS A/B runs are recorded in [`blofin-close-evaluator-backtest-results-2026-10-04.md`](blofin-close-evaluator-backtest-results-2026-10-04.md). The supplement corrects the earlier market-availability exclusion, compares all 11 tuples, and separates fixed-ATR candidates from regime-family controls. In brief, the strongest fixed-ATR candidates are SMA Crossover / ETH and Order Blocks / SPCX; several other strategies remain regime-favored or cohort/fold-dependent, and XLM Consolidation liquidates under all tested live variants.

The follow-up also fixed duplicated `argparse` options in the BloFin signal-check CLI and added a CLI smoke test. The check path and targeted Python suite pass in the current worktree, but the service has not been deployed; Go tests could not be rerun in the review environment because no Go executable is installed. Treat the updated A/B results as offline evidence until deployment/runtime parity and the global portfolio-risk interaction are verified.
