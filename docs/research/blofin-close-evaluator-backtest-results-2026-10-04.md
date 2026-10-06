# BloFin close-evaluator backtest results — 2026-10-04

> **Accounting correction (2026-10-05):** The historical PnL/PF/DD tables and evaluator recommendations below were generated before two fixed-margin accounting defects were corrected. They are retained as history, but are superseded for evaluator selection by the corrected matrix at the end of this report.

Handoff companion to `docs/research/blofin-close-evaluator-implementation-audit.md`.

## Method and assumptions

- Cohorts are separate: current live config v19 (11 matching strategies) and demo config v19 (11 matching strategies). PnL is never pooled.
- Window: 2026-05-25 through 2026-10-02 UTC, closed candles only. Train ends before 2026-08-24; holdout begins 2026-08-24, with one prior candle retained only for signal warm-up.
- For each supported strategy/cohort: current `tiered_tp_atr_regime` control vs fixed `tiered_tp_atr`, each independently tested at `sl_atr_mult=1.5` and `2.5`; BloFin TP is explicitly enabled.
- Capital, margin, leverage, direction, regime windows, certification state, and strategy CB come from the corresponding v19 config. Strategy CB is per strategy; global portfolio CB is not pooled into single-strategy runs.
- Commission: public Regular Level 0 futures taker 0.06% per market fill. Maker 0.02% was not used because simulated entries/exits are market/taker. Personal VIP tier was not checked.
- Slippage: 5 bp per fill. OHLCV/funding/result persistence is disabled (`store=False`, `save=False`); candles remain in memory.
- The 0.06% rate is an in-memory research override. Repository Go/backtest fee defaults still read 0.035% (VIP5 futures taker); the earlier 0.035% A/B run is superseded.
- `capture` is mean price-only winner capture for profitable closed legs; `avg_h` is average holding duration per closed leg.

## Run integrity

- Runs: 144/144; errors: 0; return/final-cash mismatches >0.02 pp: 0; no-trade cash drift cases: 0.
- Liquidated runs: 12. Liquidated folds: demo:bl-liquidity_sweeps-eth-30m:train (4 variants), live:live-consolidation_range-xlm-15m:holdout (4 variants), live:live-consolidation_range-xlm-15m:train (4 variants).
- The account stops at first non-positive equity; later candles cannot resurrect it. Liquidated runs are not ranked as evaluator wins.
- Explicit BloFin close composition suppresses an opposite open signal while a position is open unless an evaluator returns a positive close fraction. Thus signal-close rows are 0 in this current-live model; historical paper `signal` closes belong to a different/older path.

## BloFin candle coverage

| Symbol | Timeframe | Candles | First | Last fully closed |
|---|---:|---:|---|---|
| ADA | 30m | 6287 | 2026-05-25 00:00:00 | 2026-10-02 23:00:00 |
| DOGE | 1h | 3143 | 2026-05-25 00:00:00 | 2026-10-02 22:00:00 |
| ETH | 15m | 12575 | 2026-05-25 00:00:00 | 2026-10-02 23:30:00 |
| ETH | 30m | 6287 | 2026-05-25 00:00:00 | 2026-10-02 23:00:00 |
| HYPE | 30m | 6287 | 2026-05-25 00:00:00 | 2026-10-02 23:00:00 |
| SPX | 15m | 12575 | 2026-05-25 00:00:00 | 2026-10-02 23:30:00 |
| XLM | 15m | 12575 | 2026-05-25 00:00:00 | 2026-10-02 23:30:00 |

## Unsupported BloFin markets

- `SPCX-USDT` and `USELESS-USDT` are absent from the public BloFin SWAP catalog; candle requests return invalid instrument ID. The corresponding `order_blocks-SPCX-15m` and `ema_crossover-USELESS-15m` were not proxied to another exchange.
- Therefore 9/11 strategy tuples ran per cohort. The omitted 2 tuples × 2 cohorts × 4 variants × 2 folds equal 32 unrun cases.

## Holdout evaluator A/B

`delta_net` is fixed `tiered_tp_atr` minus `tiered_tp_atr_regime` at the same SL. Positive favors fixed tiers. Liquidated pairs are excluded from delta ranking.

| Cohort | Strategy | SL | Regime net / PF / DD% | Fixed net / PF / DD% | Delta net | Status |
|---|---|---:|---:|---:|---:|---|
| live | `live-consolidation_range-xlm-15m` | 1.5 | -100.00 / 0.921 / -100.00 | -100.00 / 0.770 / -100.00 | — | LIQUIDATED |
| live | `live-consolidation_range-xlm-15m` | 2.5 | -100.00 / 0.951 / -100.00 | -100.00 / 0.694 / -100.00 | — | LIQUIDATED |
| live | `live-heikin_ashi_ema-eth-30m` | 1.5 | 225.74 / 1.182 / -15.86 | 172.09 / 0.980 / -29.46 | -53.65 | ok |
| live | `live-heikin_ashi_ema-eth-30m` | 2.5 | 183.65 / 1.198 / -16.51 | 157.69 / 1.055 / -32.60 | -25.96 | ok |
| live | `live-heikin_ashi_ema-hype-30m` | 1.5 | 128.64 / 0.750 / -45.93 | 114.50 / 0.702 / -29.87 | -14.14 | ok |
| live | `live-heikin_ashi_ema-hype-30m` | 2.5 | 143.38 / 0.799 / -33.88 | 130.32 / 0.750 / -24.82 | -13.06 | ok |
| live | `live-liquidity_sweeps-eth-30m` | 1.5 | 71.07 / 1.186 / -22.09 | 105.47 / 1.689 / -19.04 | 34.40 | ok |
| live | `live-liquidity_sweeps-eth-30m` | 2.5 | 71.07 / 1.186 / -22.09 | 114.87 / 1.960 / -18.11 | 43.80 | ok |
| live | `live-parabolic_sar-eth-15m` | 1.5 | 249.40 / 0.939 / -19.64 | 303.72 / 1.131 / -24.10 | 54.32 | ok |
| live | `live-parabolic_sar-eth-15m` | 2.5 | 307.97 / 1.196 / -16.08 | 322.43 / 1.191 / -27.83 | 14.46 | ok |
| live | `live-regime_adaptive-spx-15m` | 1.5 | -2.13 / 0.707 / -51.89 | -8.95 / 0.642 / -50.95 | -6.82 | ok |
| live | `live-regime_adaptive-spx-15m` | 2.5 | -2.13 / 0.707 / -51.89 | -5.28 / 0.671 / -54.50 | -3.15 | ok |
| live | `live-session_breakout-ada-30m` | 1.5 | 68.62 / 0.917 / -35.20 | 39.47 / 0.760 / -31.84 | -29.15 | ok |
| live | `live-session_breakout-ada-30m` | 2.5 | 65.52 / 0.902 / -35.90 | 36.37 / 0.749 / -32.41 | -29.15 | ok |
| live | `live-sma_crossover-eth-30m` | 1.5 | 89.46 / 1.007 / -13.95 | 129.48 / 1.346 / -11.54 | 40.02 | ok |
| live | `live-sma_crossover-eth-30m` | 2.5 | 101.09 / 1.065 / -14.11 | 134.89 / 1.291 / -13.65 | 33.80 | ok |
| live | `live-stoch_rsi-doge-1h` | 1.5 | -35.74 / 0.168 / -52.57 | -35.74 / 0.168 / -52.57 | 0.00 | ok |
| live | `live-stoch_rsi-doge-1h` | 2.5 | -35.74 / 0.168 / -52.57 | -35.74 / 0.168 / -52.57 | 0.00 | ok |
| demo | `bl-consolidation_range-xlm-15m` | 1.5 | 175.10 / 0.740 / -57.26 | 192.65 / 0.695 / -57.86 | 17.55 | ok |
| demo | `bl-consolidation_range-xlm-15m` | 2.5 | 184.19 / 0.757 / -57.30 | 145.77 / 0.662 / -58.08 | -38.42 | ok |
| demo | `bl-heikin_ashi_ema-eth-30m` | 1.5 | 84.30 / 0.797 / -15.05 | 137.57 / 0.912 / -9.47 | 53.27 | ok |
| demo | `bl-heikin_ashi_ema-eth-30m` | 2.5 | 63.08 / 0.809 / -16.12 | 81.31 / 0.853 / -9.58 | 18.23 | ok |
| demo | `bl-heikin_ashi_ema-hype-30m` | 1.5 | 128.64 / 0.750 / -7.32 | 114.50 / 0.702 / -7.01 | -14.14 | ok |
| demo | `bl-heikin_ashi_ema-hype-30m` | 2.5 | 143.38 / 0.799 / -5.77 | 130.32 / 0.750 / -5.41 | -13.06 | ok |
| demo | `bl-liquidity_sweeps-eth-30m` | 1.5 | -49.48 / 0.561 / -64.85 | -26.47 / 0.613 / -64.85 | 23.01 | ok |
| demo | `bl-liquidity_sweeps-eth-30m` | 2.5 | -49.48 / 0.561 / -64.85 | -26.47 / 0.613 / -64.85 | 23.01 | ok |
| demo | `bl-parabolic_sar-eth-15m` | 1.5 | -105.91 / 0.451 / -16.90 | -77.71 / 0.496 / -17.58 | 28.20 | ok |
| demo | `bl-parabolic_sar-eth-15m` | 2.5 | -130.80 / 0.436 / -17.40 | -98.06 / 0.482 / -18.01 | 32.74 | ok |
| demo | `bl-regime_adaptive-spx-15m` | 1.5 | 310.75 / 0.934 / -64.66 | 270.70 / 0.896 / -70.18 | -40.05 | ok |
| demo | `bl-regime_adaptive-spx-15m` | 2.5 | 292.13 / 0.940 / -65.15 | 313.52 / 0.967 / -67.13 | 21.39 | ok |
| demo | `bl-session_breakout-ada-30m` | 1.5 | -3.53 / 0.668 / -56.13 | -62.98 / 0.528 / -57.77 | -59.45 | ok |
| demo | `bl-session_breakout-ada-30m` | 2.5 | -5.51 / 0.664 / -56.13 | -64.96 / 0.526 / -57.77 | -59.45 | ok |
| demo | `bl-sma_crossover-eth-30m` | 1.5 | -22.07 / 0.434 / -6.14 | -4.26 / 0.575 / -4.89 | 17.81 | ok |
| demo | `bl-sma_crossover-eth-30m` | 2.5 | -6.29 / 0.478 / -4.77 | 10.54 / 0.612 / -4.82 | 16.83 | ok |
| demo | `bl-stoch_rsi-doge-1h` | 1.5 | -32.29 / 0.614 / -9.41 | 5.27 / 0.672 / -8.09 | 37.56 | ok |
| demo | `bl-stoch_rsi-doge-1h` | 2.5 | -26.35 / 0.630 / -9.41 | 11.21 / 0.692 / -8.09 | 37.56 | ok |

### Holdout comparison counts

- live: fixed wins 6, regime wins 8, ties 2, liquidated pairs 2.
- demo: fixed wins 12, regime wins 6, ties 0, liquidated pairs 0.

## Independent SL comparison on holdout

`delta_net` is SL 2.5 minus SL 1.5 for the same evaluator.

| Cohort | Strategy | Evaluator | Net SL 1.5 | Net SL 2.5 | Delta net | Status |
|---|---|---|---:|---:|---:|---|
| live | `live-consolidation_range-xlm-15m` | `tiered_tp_atr_regime` | -100.00 | -100.00 | — | LIQUIDATED |
| live | `live-consolidation_range-xlm-15m` | `tiered_tp_atr` | -100.00 | -100.00 | — | LIQUIDATED |
| live | `live-heikin_ashi_ema-eth-30m` | `tiered_tp_atr_regime` | 225.74 | 183.65 | -42.09 | ok |
| live | `live-heikin_ashi_ema-eth-30m` | `tiered_tp_atr` | 172.09 | 157.69 | -14.40 | ok |
| live | `live-heikin_ashi_ema-hype-30m` | `tiered_tp_atr_regime` | 128.64 | 143.38 | 14.74 | ok |
| live | `live-heikin_ashi_ema-hype-30m` | `tiered_tp_atr` | 114.50 | 130.32 | 15.82 | ok |
| live | `live-liquidity_sweeps-eth-30m` | `tiered_tp_atr_regime` | 71.07 | 71.07 | 0.00 | ok |
| live | `live-liquidity_sweeps-eth-30m` | `tiered_tp_atr` | 105.47 | 114.87 | 9.40 | ok |
| live | `live-parabolic_sar-eth-15m` | `tiered_tp_atr_regime` | 249.40 | 307.97 | 58.57 | ok |
| live | `live-parabolic_sar-eth-15m` | `tiered_tp_atr` | 303.72 | 322.43 | 18.71 | ok |
| live | `live-regime_adaptive-spx-15m` | `tiered_tp_atr_regime` | -2.13 | -2.13 | 0.00 | ok |
| live | `live-regime_adaptive-spx-15m` | `tiered_tp_atr` | -8.95 | -5.28 | 3.67 | ok |
| live | `live-session_breakout-ada-30m` | `tiered_tp_atr_regime` | 68.62 | 65.52 | -3.10 | ok |
| live | `live-session_breakout-ada-30m` | `tiered_tp_atr` | 39.47 | 36.37 | -3.10 | ok |
| live | `live-sma_crossover-eth-30m` | `tiered_tp_atr_regime` | 89.46 | 101.09 | 11.63 | ok |
| live | `live-sma_crossover-eth-30m` | `tiered_tp_atr` | 129.48 | 134.89 | 5.41 | ok |
| live | `live-stoch_rsi-doge-1h` | `tiered_tp_atr_regime` | -35.74 | -35.74 | 0.00 | ok |
| live | `live-stoch_rsi-doge-1h` | `tiered_tp_atr` | -35.74 | -35.74 | 0.00 | ok |
| demo | `bl-consolidation_range-xlm-15m` | `tiered_tp_atr_regime` | 175.10 | 184.19 | 9.09 | ok |
| demo | `bl-consolidation_range-xlm-15m` | `tiered_tp_atr` | 192.65 | 145.77 | -46.88 | ok |
| demo | `bl-heikin_ashi_ema-eth-30m` | `tiered_tp_atr_regime` | 84.30 | 63.08 | -21.22 | ok |
| demo | `bl-heikin_ashi_ema-eth-30m` | `tiered_tp_atr` | 137.57 | 81.31 | -56.26 | ok |
| demo | `bl-heikin_ashi_ema-hype-30m` | `tiered_tp_atr_regime` | 128.64 | 143.38 | 14.74 | ok |
| demo | `bl-heikin_ashi_ema-hype-30m` | `tiered_tp_atr` | 114.50 | 130.32 | 15.82 | ok |
| demo | `bl-liquidity_sweeps-eth-30m` | `tiered_tp_atr_regime` | -49.48 | -49.48 | 0.00 | ok |
| demo | `bl-liquidity_sweeps-eth-30m` | `tiered_tp_atr` | -26.47 | -26.47 | 0.00 | ok |
| demo | `bl-parabolic_sar-eth-15m` | `tiered_tp_atr_regime` | -105.91 | -130.80 | -24.89 | ok |
| demo | `bl-parabolic_sar-eth-15m` | `tiered_tp_atr` | -77.71 | -98.06 | -20.35 | ok |
| demo | `bl-regime_adaptive-spx-15m` | `tiered_tp_atr_regime` | 310.75 | 292.13 | -18.62 | ok |
| demo | `bl-regime_adaptive-spx-15m` | `tiered_tp_atr` | 270.70 | 313.52 | 42.82 | ok |
| demo | `bl-session_breakout-ada-30m` | `tiered_tp_atr_regime` | -3.53 | -5.51 | -1.98 | ok |
| demo | `bl-session_breakout-ada-30m` | `tiered_tp_atr` | -62.98 | -64.96 | -1.98 | ok |
| demo | `bl-sma_crossover-eth-30m` | `tiered_tp_atr_regime` | -22.07 | -6.29 | 15.78 | ok |
| demo | `bl-sma_crossover-eth-30m` | `tiered_tp_atr` | -4.26 | 10.54 | 14.80 | ok |
| demo | `bl-stoch_rsi-doge-1h` | `tiered_tp_atr_regime` | -32.29 | -26.35 | 5.94 | ok |
| demo | `bl-stoch_rsi-doge-1h` | `tiered_tp_atr` | 5.27 | 11.21 | 5.94 | ok |

## Per-run metrics

| Cohort | Strategy | Fold | Evaluator | SL | Capital | Margin | Leverage | Net USD | Return % | PF | Max DD % | Trades | CB n / net USD | Signal n / net USD | Avg h | Capture | Liquidated | Policy |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|---|
| demo | `bl-consolidation_range-xlm-15m` | holdout | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 75.0 | 192.65 | 19.27 | 0.695 | -57.86 | 180 | 73 / -553.32 | 0 / 0.00 | 5.27 | 0.859 (101) | False | default_off* |
| demo | `bl-consolidation_range-xlm-15m` | holdout | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 75.0 | 145.77 | 14.58 | 0.662 | -58.08 | 163 | 75 / -557.98 | 0 / 0.00 | 5.84 | 0.857 (91) | False | default_off* |
| demo | `bl-consolidation_range-xlm-15m` | holdout | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 75.0 | 175.10 | 17.51 | 0.740 | -57.26 | 180 | 73 / -518.20 | 0 / 0.00 | 5.53 | 0.880 (103) | False | default_off* |
| demo | `bl-consolidation_range-xlm-15m` | holdout | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 75.0 | 184.19 | 18.42 | 0.757 | -57.30 | 172 | 79 / -563.44 | 0 / 0.00 | 5.69 | 0.880 (102) | False | default_off* |
| demo | `bl-consolidation_range-xlm-15m` | train | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 75.0 | 234.93 | 23.49 | 0.544 | -76.81 | 490 | 190 / -1417.56 | 0 / 0.00 | 3.36 | 0.873 (268) | False | default_off* |
| demo | `bl-consolidation_range-xlm-15m` | train | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 75.0 | 265.17 | 26.52 | 0.579 | -73.99 | 437 | 198 / -1471.78 | 0 / 0.00 | 3.88 | 0.875 (252) | False | default_off* |
| demo | `bl-consolidation_range-xlm-15m` | train | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 75.0 | 236.87 | 23.69 | 0.588 | -65.82 | 448 | 168 / -1217.11 | 0 / 0.00 | 4.18 | 0.877 (249) | False | default_off* |
| demo | `bl-consolidation_range-xlm-15m` | train | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 75.0 | 332.09 | 33.21 | 0.634 | -60.64 | 433 | 182 / -1383.41 | 0 / 0.00 | 4.93 | 0.883 (256) | False | default_off* |
| demo | `bl-heikin_ashi_ema-eth-30m` | holdout | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 150.0 | 137.57 | 13.76 | 0.912 | -9.47 | 84 | 34 / -368.20 | 0 / 0.00 | 9.67 | 0.800 (43) | False | none |
| demo | `bl-heikin_ashi_ema-eth-30m` | holdout | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 150.0 | 81.31 | 8.13 | 0.853 | -9.58 | 69 | 31 / -332.67 | 0 / 0.00 | 11.73 | 0.790 (35) | False | none |
| demo | `bl-heikin_ashi_ema-eth-30m` | holdout | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 150.0 | 84.30 | 8.43 | 0.797 | -15.05 | 92 | 34 / -390.20 | 0 / 0.00 | 8.05 | 0.752 (50) | False | none |
| demo | `bl-heikin_ashi_ema-eth-30m` | holdout | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 150.0 | 63.08 | 6.31 | 0.809 | -16.12 | 73 | 31 / -350.32 | 0 / 0.00 | 11.02 | 0.761 (40) | False | none |
| demo | `bl-heikin_ashi_ema-eth-30m` | train | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 150.0 | 156.80 | 15.68 | 0.828 | -18.67 | 181 | 86 / -881.88 | 0 / 0.00 | 8.27 | 0.790 (82) | False | none |
| demo | `bl-heikin_ashi_ema-eth-30m` | train | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 150.0 | 118.93 | 11.89 | 0.817 | -19.46 | 165 | 88 / -898.91 | 0 / 0.00 | 9.34 | 0.801 (74) | False | none |
| demo | `bl-heikin_ashi_ema-eth-30m` | train | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 150.0 | -17.36 | -1.74 | 0.687 | -28.95 | 201 | 93 / -970.78 | 0 / 0.00 | 6.35 | 0.758 (92) | False | none |
| demo | `bl-heikin_ashi_ema-eth-30m` | train | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 150.0 | 9.12 | 0.91 | 0.711 | -28.60 | 183 | 90 / -944.43 | 0 / 0.00 | 7.53 | 0.763 (87) | False | none |
| demo | `bl-heikin_ashi_ema-hype-30m` | holdout | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 75.0 | 114.50 | 11.45 | 0.702 | -7.01 | 94 | 33 / -297.54 | 0 / 0.00 | 7.22 | 0.877 (52) | False | none |
| demo | `bl-heikin_ashi_ema-hype-30m` | holdout | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 75.0 | 130.32 | 13.03 | 0.750 | -5.41 | 91 | 33 / -301.18 | 0 / 0.00 | 7.69 | 0.885 (53) | False | none |
| demo | `bl-heikin_ashi_ema-hype-30m` | holdout | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 75.0 | 128.64 | 12.86 | 0.750 | -7.32 | 94 | 30 / -271.44 | 0 / 0.00 | 7.39 | 0.854 (53) | False | none |
| demo | `bl-heikin_ashi_ema-hype-30m` | holdout | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 75.0 | 143.38 | 14.34 | 0.799 | -5.77 | 91 | 30 / -275.08 | 0 / 0.00 | 7.92 | 0.862 (54) | False | none |
| demo | `bl-heikin_ashi_ema-hype-30m` | train | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 75.0 | 244.66 | 24.47 | 0.808 | -18.75 | 214 | 90 / -847.95 | 0 / 0.00 | 7.52 | 0.870 (107) | False | none |
| demo | `bl-heikin_ashi_ema-hype-30m` | train | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 75.0 | 231.41 | 23.14 | 0.808 | -19.02 | 205 | 94 / -874.45 | 0 / 0.00 | 7.98 | 0.865 (104) | False | none |
| demo | `bl-heikin_ashi_ema-hype-30m` | train | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 75.0 | 268.50 | 26.85 | 0.882 | -16.33 | 226 | 93 / -853.79 | 0 / 0.00 | 6.51 | 0.848 (121) | False | none |
| demo | `bl-heikin_ashi_ema-hype-30m` | train | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 75.0 | 256.50 | 25.65 | 0.883 | -16.33 | 217 | 97 / -880.96 | 0 / 0.00 | 7.00 | 0.845 (117) | False | none |
| demo | `bl-liquidity_sweeps-eth-30m` | holdout | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 150.0 | -26.47 | -2.65 | 0.613 | -64.85 | 37 | 18 / -232.90 | 0 / 0.00 | 7.39 | 0.774 (21) | False | default_off* |
| demo | `bl-liquidity_sweeps-eth-30m` | holdout | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 150.0 | -26.47 | -2.65 | 0.613 | -64.85 | 37 | 18 / -232.90 | 0 / 0.00 | 7.39 | 0.774 (21) | False | default_off* |
| demo | `bl-liquidity_sweeps-eth-30m` | holdout | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 150.0 | -49.48 | -4.95 | 0.561 | -64.85 | 37 | 19 / -243.72 | 0 / 0.00 | 6.97 | 0.776 (19) | False | default_off* |
| demo | `bl-liquidity_sweeps-eth-30m` | holdout | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 150.0 | -49.48 | -4.95 | 0.561 | -64.85 | 37 | 19 / -243.72 | 0 / 0.00 | 6.97 | 0.776 (19) | False | default_off* |
| demo | `bl-liquidity_sweeps-eth-30m` | train | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 150.0 | -1000.00 | -100.00 | 0.555 | -100.00 | 52 | 30 / -278.83 | 0 / 0.00 | 5.70 | 0.793 (25) | True | default_off* |
| demo | `bl-liquidity_sweeps-eth-30m` | train | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 150.0 | -1000.00 | -100.00 | 0.549 | -100.00 | 52 | 31 / -287.82 | 0 / 0.00 | 5.76 | 0.793 (25) | True | default_off* |
| demo | `bl-liquidity_sweeps-eth-30m` | train | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 150.0 | -1000.00 | -100.00 | 0.652 | -100.00 | 55 | 30 / -279.93 | 0 / 0.00 | 5.94 | 0.809 (28) | True | default_off* |
| demo | `bl-liquidity_sweeps-eth-30m` | train | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 150.0 | -1000.00 | -100.00 | 0.645 | -100.00 | 55 | 31 / -288.92 | 0 / 0.00 | 5.99 | 0.809 (28) | True | default_off* |
| demo | `bl-parabolic_sar-eth-15m` | holdout | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 150.0 | -77.71 | -7.77 | 0.496 | -17.58 | 150 | 65 / -657.23 | 0 / 0.00 | 4.96 | 0.807 (72) | False | none |
| demo | `bl-parabolic_sar-eth-15m` | holdout | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 150.0 | -98.06 | -9.81 | 0.482 | -18.01 | 150 | 71 / -706.08 | 0 / 0.00 | 5.05 | 0.807 (72) | False | none |
| demo | `bl-parabolic_sar-eth-15m` | holdout | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 150.0 | -105.91 | -10.59 | 0.451 | -16.90 | 164 | 64 / -630.96 | 0 / 0.00 | 3.43 | 0.734 (80) | False | none |
| demo | `bl-parabolic_sar-eth-15m` | holdout | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 150.0 | -130.80 | -13.08 | 0.436 | -17.40 | 164 | 70 / -684.94 | 0 / 0.00 | 3.53 | 0.734 (80) | False | none |
| demo | `bl-parabolic_sar-eth-15m` | train | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 150.0 | -106.88 | -10.69 | 0.520 | -32.37 | 351 | 148 / -1482.74 | 0 / 0.00 | 5.02 | 0.794 (173) | False | none |
| demo | `bl-parabolic_sar-eth-15m` | train | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 150.0 | -92.24 | -9.22 | 0.538 | -32.09 | 326 | 150 / -1484.96 | 0 / 0.00 | 5.66 | 0.801 (166) | False | none |
| demo | `bl-parabolic_sar-eth-15m` | train | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 150.0 | -250.18 | -25.02 | 0.492 | -38.59 | 356 | 150 / -1485.20 | 0 / 0.00 | 3.91 | 0.775 (169) | False | none |
| demo | `bl-parabolic_sar-eth-15m` | train | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 150.0 | -189.67 | -18.97 | 0.514 | -36.57 | 349 | 154 / -1515.66 | 0 / 0.00 | 4.27 | 0.774 (175) | False | none |
| demo | `bl-regime_adaptive-spx-15m` | holdout | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 75.0 | 270.70 | 27.07 | 0.896 | -70.18 | 160 | 71 / -595.80 | 0 / 0.00 | 3.37 | 0.877 (94) | False | default_off* |
| demo | `bl-regime_adaptive-spx-15m` | holdout | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 75.0 | 313.52 | 31.35 | 0.967 | -67.13 | 153 | 67 / -559.20 | 0 / 0.00 | 3.57 | 0.876 (95) | False | default_off* |
| demo | `bl-regime_adaptive-spx-15m` | holdout | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 75.0 | 310.75 | 31.07 | 0.934 | -64.66 | 180 | 74 / -641.19 | 0 / 0.00 | 3.33 | 0.891 (108) | False | default_off* |
| demo | `bl-regime_adaptive-spx-15m` | holdout | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 75.0 | 292.13 | 29.21 | 0.940 | -65.15 | 173 | 75 / -634.80 | 0 / 0.00 | 3.26 | 0.886 (105) | False | default_off* |
| demo | `bl-regime_adaptive-spx-15m` | train | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 75.0 | 371.86 | 37.19 | 0.686 | -65.36 | 389 | 151 / -1326.46 | 0 / 0.00 | 3.10 | 0.862 (210) | False | default_off* |
| demo | `bl-regime_adaptive-spx-15m` | train | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 75.0 | 330.37 | 33.04 | 0.676 | -63.17 | 369 | 160 / -1379.44 | 0 / 0.00 | 3.37 | 0.860 (205) | False | default_off* |
| demo | `bl-regime_adaptive-spx-15m` | train | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 75.0 | 370.08 | 37.01 | 0.706 | -59.11 | 402 | 150 / -1285.46 | 0 / 0.00 | 3.26 | 0.871 (225) | False | default_off* |
| demo | `bl-regime_adaptive-spx-15m` | train | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 75.0 | 392.69 | 39.27 | 0.727 | -59.39 | 396 | 159 / -1358.15 | 0 / 0.00 | 3.61 | 0.874 (229) | False | default_off* |
| demo | `bl-session_breakout-ada-30m` | holdout | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 75.0 | -62.98 | -6.30 | 0.528 | -57.77 | 71 | 40 / -371.68 | 0 / 0.00 | 7.23 | 0.825 (32) | False | default_off* |
| demo | `bl-session_breakout-ada-30m` | holdout | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 75.0 | -64.96 | -6.50 | 0.526 | -57.77 | 71 | 41 / -377.73 | 0 / 0.00 | 7.24 | 0.825 (32) | False | default_off* |
| demo | `bl-session_breakout-ada-30m` | holdout | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 75.0 | -3.53 | -0.35 | 0.668 | -56.13 | 71 | 37 / -340.39 | 0 / 0.00 | 8.89 | 0.851 (35) | False | default_off* |
| demo | `bl-session_breakout-ada-30m` | holdout | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 75.0 | -5.51 | -0.55 | 0.664 | -56.13 | 71 | 38 / -346.44 | 0 / 0.00 | 8.90 | 0.851 (35) | False | default_off* |
| demo | `bl-session_breakout-ada-30m` | train | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 75.0 | 225.31 | 22.53 | 0.854 | -68.03 | 157 | 73 / -540.59 | 0 / 0.00 | 5.88 | 0.855 (85) | False | default_off* |
| demo | `bl-session_breakout-ada-30m` | train | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 75.0 | 215.94 | 21.59 | 0.848 | -66.89 | 153 | 76 / -548.88 | 0 / 0.00 | 6.10 | 0.855 (84) | False | default_off* |
| demo | `bl-session_breakout-ada-30m` | train | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 75.0 | 296.17 | 29.62 | 0.954 | -62.82 | 172 | 71 / -580.00 | 0 / 0.00 | 5.94 | 0.857 (95) | False | default_off* |
| demo | `bl-session_breakout-ada-30m` | train | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 75.0 | 324.18 | 32.42 | 1.011 | -62.44 | 166 | 71 / -572.27 | 0 / 0.00 | 6.53 | 0.857 (98) | False | default_off* |
| demo | `bl-sma_crossover-eth-30m` | holdout | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 150.0 | -4.26 | -0.43 | 0.575 | -4.89 | 34 | 14 / -140.18 | 0 / 0.00 | 8.16 | 0.825 (14) | False | none |
| demo | `bl-sma_crossover-eth-30m` | holdout | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 150.0 | 10.54 | 1.05 | 0.612 | -4.82 | 35 | 15 / -147.87 | 0 / 0.00 | 8.31 | 0.814 (16) | False | none |
| demo | `bl-sma_crossover-eth-30m` | holdout | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 150.0 | -22.07 | -2.21 | 0.434 | -6.14 | 33 | 12 / -122.96 | 0 / 0.00 | 5.32 | 0.703 (16) | False | none |
| demo | `bl-sma_crossover-eth-30m` | holdout | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 150.0 | -6.29 | -0.63 | 0.478 | -4.77 | 34 | 13 / -130.65 | 0 / 0.00 | 5.54 | 0.707 (18) | False | none |
| demo | `bl-sma_crossover-eth-30m` | train | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 150.0 | -82.74 | -8.27 | 0.482 | -13.95 | 72 | 34 / -317.62 | 0 / 0.00 | 6.07 | 0.766 (28) | False | none |
| demo | `bl-sma_crossover-eth-30m` | train | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 150.0 | -76.48 | -7.65 | 0.481 | -13.32 | 73 | 37 / -342.70 | 0 / 0.00 | 7.04 | 0.750 (30) | False | none |
| demo | `bl-sma_crossover-eth-30m` | train | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 150.0 | -143.79 | -14.38 | 0.340 | -17.29 | 71 | 34 / -317.62 | 0 / 0.00 | 5.39 | 0.750 (26) | False | none |
| demo | `bl-sma_crossover-eth-30m` | train | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 150.0 | -137.54 | -13.75 | 0.340 | -17.13 | 72 | 37 / -342.70 | 0 / 0.00 | 6.39 | 0.734 (28) | False | none |
| demo | `bl-stoch_rsi-doge-1h` | holdout | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 75.0 | 5.27 | 0.53 | 0.672 | -8.09 | 39 | 19 / -202.17 | 0 / 0.00 | 21.13 | 0.844 (18) | False | none |
| demo | `bl-stoch_rsi-doge-1h` | holdout | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 75.0 | 11.21 | 1.12 | 0.692 | -8.09 | 38 | 18 / -195.61 | 0 / 0.00 | 21.71 | 0.844 (18) | False | none |
| demo | `bl-stoch_rsi-doge-1h` | holdout | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 75.0 | -32.29 | -3.23 | 0.614 | -9.41 | 39 | 20 / -226.43 | 0 / 0.00 | 18.87 | 0.884 (17) | False | none |
| demo | `bl-stoch_rsi-doge-1h` | holdout | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 75.0 | -26.35 | -2.64 | 0.630 | -9.41 | 38 | 19 / -219.87 | 0 / 0.00 | 19.39 | 0.884 (17) | False | none |
| demo | `bl-stoch_rsi-doge-1h` | train | `tiered_tp_atr` | 1.5 | 1000.00 | 10.00 | 75.0 | 36.84 | 3.68 | 0.735 | -12.72 | 79 | 36 / -343.75 | 0 / 0.00 | 17.94 | 0.860 (36) | False | none |
| demo | `bl-stoch_rsi-doge-1h` | train | `tiered_tp_atr` | 2.5 | 1000.00 | 10.00 | 75.0 | 65.03 | 6.50 | 0.792 | -11.73 | 76 | 36 / -341.98 | 0 / 0.00 | 20.87 | 0.860 (37) | False | none |
| demo | `bl-stoch_rsi-doge-1h` | train | `tiered_tp_atr_regime` | 1.5 | 1000.00 | 10.00 | 75.0 | -4.46 | -0.45 | 0.661 | -15.31 | 79 | 39 / -375.63 | 0 / 0.00 | 18.39 | 0.865 (36) | False | none |
| demo | `bl-stoch_rsi-doge-1h` | train | `tiered_tp_atr_regime` | 2.5 | 1000.00 | 10.00 | 75.0 | -3.47 | -0.35 | 0.662 | -15.21 | 78 | 40 / -383.94 | 0 / 0.00 | 18.97 | 0.865 (36) | False | none |
| live | `live-consolidation_range-xlm-15m` | holdout | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | -100.00 | -100.00 | 0.770 | -100.00 | 20 | 8 / -62.70 | 0 / 0.00 | 5.66 | 0.875 (10) | True | default_off* |
| live | `live-consolidation_range-xlm-15m` | holdout | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | -100.00 | -100.00 | 0.694 | -100.00 | 18 | 7 / -56.27 | 0 / 0.00 | 5.50 | 0.894 (9) | True | default_off* |
| live | `live-consolidation_range-xlm-15m` | holdout | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | -100.00 | -100.00 | 0.921 | -100.00 | 23 | 8 / -65.34 | 0 / 0.00 | 4.76 | 0.882 (13) | True | default_off* |
| live | `live-consolidation_range-xlm-15m` | holdout | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | -100.00 | -100.00 | 0.951 | -100.00 | 22 | 7 / -57.27 | 0 / 0.00 | 4.55 | 0.895 (13) | True | default_off* |
| live | `live-consolidation_range-xlm-15m` | train | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | -100.00 | -100.00 | 0.117 | -100.00 | 8 | 3 / -21.40 | 0 / 0.00 | 0.72 | 0.964 (2) | True | default_off* |
| live | `live-consolidation_range-xlm-15m` | train | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | -100.00 | -100.00 | 0.129 | -100.00 | 6 | 4 / -33.67 | 0 / 0.00 | 0.96 | 0.964 (2) | True | default_off* |
| live | `live-consolidation_range-xlm-15m` | train | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | -100.00 | -100.00 | 0.347 | -100.00 | 8 | 3 / -21.40 | 0 / 0.00 | 1.41 | 0.907 (2) | True | default_off* |
| live | `live-consolidation_range-xlm-15m` | train | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | -100.00 | -100.00 | 0.380 | -100.00 | 6 | 4 / -33.67 | 0 / 0.00 | 1.88 | 0.907 (2) | True | default_off* |
| live | `live-heikin_ashi_ema-eth-30m` | holdout | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | 172.09 | 172.09 | 0.980 | -29.46 | 76 | 20 / -162.31 | 0 / 0.00 | 12.21 | 0.811 (46) | False | none |
| live | `live-heikin_ashi_ema-eth-30m` | holdout | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | 157.69 | 157.69 | 1.055 | -32.60 | 60 | 16 / -143.71 | 0 / 0.00 | 16.70 | 0.805 (40) | False | none |
| live | `live-heikin_ashi_ema-eth-30m` | holdout | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | 225.74 | 225.74 | 1.182 | -15.86 | 84 | 18 / -147.69 | 0 / 0.00 | 10.82 | 0.763 (56) | False | none |
| live | `live-heikin_ashi_ema-eth-30m` | holdout | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | 183.65 | 183.65 | 1.198 | -16.51 | 64 | 15 / -125.63 | 0 / 0.00 | 15.27 | 0.774 (44) | False | none |
| live | `live-heikin_ashi_ema-eth-30m` | train | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | -51.27 | -51.27 | 0.100 | -58.46 | 16 | 7 / -54.42 | 0 / 0.00 | 9.25 | 0.798 (4) | False | none |
| live | `live-heikin_ashi_ema-eth-30m` | train | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | -47.46 | -47.46 | 0.106 | -54.53 | 15 | 8 / -55.52 | 0 / 0.00 | 10.33 | 0.798 (4) | False | none |
| live | `live-heikin_ashi_ema-eth-30m` | train | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | -49.87 | -49.87 | 0.132 | -54.72 | 21 | 8 / -67.75 | 0 / 0.00 | 6.76 | 0.751 (8) | False | none |
| live | `live-heikin_ashi_ema-eth-30m` | train | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | -52.94 | -52.94 | 0.128 | -63.05 | 21 | 10 / -77.07 | 0 / 0.00 | 7.14 | 0.751 (8) | False | none |
| live | `live-heikin_ashi_ema-hype-30m` | holdout | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | 114.50 | 114.50 | 0.702 | -29.87 | 94 | 33 / -297.54 | 0 / 0.00 | 7.22 | 0.877 (52) | False | none |
| live | `live-heikin_ashi_ema-hype-30m` | holdout | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | 130.32 | 130.32 | 0.750 | -24.82 | 91 | 33 / -301.18 | 0 / 0.00 | 7.69 | 0.885 (53) | False | none |
| live | `live-heikin_ashi_ema-hype-30m` | holdout | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | 128.64 | 128.64 | 0.750 | -45.93 | 94 | 30 / -271.44 | 0 / 0.00 | 7.39 | 0.854 (53) | False | none |
| live | `live-heikin_ashi_ema-hype-30m` | holdout | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | 143.38 | 143.38 | 0.799 | -33.88 | 91 | 30 / -275.08 | 0 / 0.00 | 7.92 | 0.862 (54) | False | none |
| live | `live-heikin_ashi_ema-hype-30m` | train | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | -51.86 | -51.86 | 0.083 | -53.89 | 7 | 6 / -60.90 | 0 / 0.00 | 2.64 | 0.752 (1) | False | none |
| live | `live-heikin_ashi_ema-hype-30m` | train | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | -51.86 | -51.86 | 0.083 | -53.89 | 7 | 6 / -60.90 | 0 / 0.00 | 2.64 | 0.752 (1) | False | none |
| live | `live-heikin_ashi_ema-hype-30m` | train | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | -51.86 | -51.86 | 0.083 | -53.89 | 7 | 6 / -60.90 | 0 / 0.00 | 2.64 | 0.752 (1) | False | none |
| live | `live-heikin_ashi_ema-hype-30m` | train | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | -51.86 | -51.86 | 0.083 | -53.89 | 7 | 6 / -60.90 | 0 / 0.00 | 2.64 | 0.752 (1) | False | none |
| live | `live-liquidity_sweeps-eth-30m` | holdout | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | 105.47 | 105.47 | 1.689 | -19.04 | 25 | 4 / -41.87 | 0 / 0.00 | 13.86 | 0.835 (20) | False | none |
| live | `live-liquidity_sweeps-eth-30m` | holdout | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | 114.87 | 114.87 | 1.960 | -18.11 | 25 | 4 / -41.87 | 0 / 0.00 | 15.74 | 0.839 (21) | False | none |
| live | `live-liquidity_sweeps-eth-30m` | holdout | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | 71.07 | 71.07 | 1.186 | -22.09 | 24 | 6 / -54.87 | 0 / 0.00 | 13.15 | 0.848 (17) | False | none |
| live | `live-liquidity_sweeps-eth-30m` | holdout | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | 71.07 | 71.07 | 1.186 | -22.09 | 24 | 6 / -54.87 | 0 / 0.00 | 13.15 | 0.848 (17) | False | none |
| live | `live-liquidity_sweeps-eth-30m` | train | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | 138.53 | 138.53 | 1.082 | -36.84 | 51 | 12 / -108.63 | 0 / 0.00 | 13.81 | 0.826 (34) | False | none |
| live | `live-liquidity_sweeps-eth-30m` | train | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | 139.21 | 139.21 | 1.088 | -38.93 | 50 | 13 / -115.82 | 0 / 0.00 | 14.17 | 0.826 (34) | False | none |
| live | `live-liquidity_sweeps-eth-30m` | train | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | 161.32 | 161.32 | 1.220 | -34.82 | 54 | 12 / -108.63 | 0 / 0.00 | 13.84 | 0.836 (37) | False | none |
| live | `live-liquidity_sweeps-eth-30m` | train | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | 162.00 | 162.00 | 1.226 | -36.28 | 53 | 13 / -115.82 | 0 / 0.00 | 14.18 | 0.836 (37) | False | none |
| live | `live-parabolic_sar-eth-15m` | holdout | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | 303.72 | 303.72 | 1.131 | -24.10 | 109 | 10 / -89.40 | 0 / 0.00 | 9.44 | 0.824 (71) | False | none |
| live | `live-parabolic_sar-eth-15m` | holdout | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | 322.43 | 322.43 | 1.191 | -27.83 | 104 | 18 / -150.62 | 0 / 0.00 | 10.45 | 0.814 (73) | False | none |
| live | `live-parabolic_sar-eth-15m` | holdout | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | 249.40 | 249.40 | 0.939 | -19.64 | 116 | 12 / -94.08 | 0 / 0.00 | 7.61 | 0.753 (73) | False | none |
| live | `live-parabolic_sar-eth-15m` | holdout | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | 307.97 | 307.97 | 1.196 | -16.08 | 108 | 18 / -138.62 | 0 / 0.00 | 9.42 | 0.755 (78) | False | none |
| live | `live-parabolic_sar-eth-15m` | train | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | -34.16 | -34.16 | 0.224 | -51.83 | 32 | 10 / -97.85 | 0 / 0.00 | 7.61 | 0.844 (14) | False | none |
| live | `live-parabolic_sar-eth-15m` | train | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | -35.78 | -35.78 | 0.222 | -50.69 | 31 | 13 / -111.88 | 0 / 0.00 | 8.04 | 0.822 (14) | False | none |
| live | `live-parabolic_sar-eth-15m` | train | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | -40.81 | -40.81 | 0.197 | -50.97 | 31 | 9 / -79.70 | 0 / 0.00 | 4.43 | 0.783 (13) | False | none |
| live | `live-parabolic_sar-eth-15m` | train | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | -40.70 | -40.70 | 0.193 | -50.88 | 24 | 11 / -89.97 | 0 / 0.00 | 4.44 | 0.752 (10) | False | none |
| live | `live-regime_adaptive-spx-15m` | holdout | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | -8.95 | -8.95 | 0.642 | -50.95 | 36 | 20 / -185.98 | 0 / 0.00 | 3.11 | 0.879 (15) | False | none |
| live | `live-regime_adaptive-spx-15m` | holdout | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | -5.28 | -5.28 | 0.671 | -54.50 | 36 | 20 / -194.28 | 0 / 0.00 | 3.38 | 0.876 (16) | False | none |
| live | `live-regime_adaptive-spx-15m` | holdout | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | -2.13 | -2.13 | 0.707 | -51.89 | 38 | 20 / -192.00 | 0 / 0.00 | 2.84 | 0.901 (18) | False | none |
| live | `live-regime_adaptive-spx-15m` | holdout | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | -2.13 | -2.13 | 0.707 | -51.89 | 38 | 20 / -192.00 | 0 / 0.00 | 2.84 | 0.901 (18) | False | none |
| live | `live-regime_adaptive-spx-15m` | train | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | -42.21 | -42.21 | 0.417 | -56.89 | 46 | 19 / -211.14 | 0 / 0.00 | 2.45 | 0.914 (21) | False | none |
| live | `live-regime_adaptive-spx-15m` | train | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | -45.11 | -45.11 | 0.259 | -52.78 | 15 | 9 / -88.98 | 0 / 0.00 | 3.03 | 0.916 (5) | False | none |
| live | `live-regime_adaptive-spx-15m` | train | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | -45.00 | -45.00 | 0.245 | -52.68 | 10 | 7 / -70.22 | 0 / 0.00 | 1.70 | 0.928 (3) | False | none |
| live | `live-regime_adaptive-spx-15m` | train | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | -45.00 | -45.00 | 0.245 | -52.68 | 10 | 7 / -70.22 | 0 / 0.00 | 1.70 | 0.928 (3) | False | none |
| live | `live-session_breakout-ada-30m` | holdout | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | 39.47 | 39.47 | 0.760 | -31.84 | 45 | 20 / -197.07 | 0 / 0.00 | 5.88 | 0.836 (24) | False | none |
| live | `live-session_breakout-ada-30m` | holdout | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | 36.37 | 36.37 | 0.749 | -32.41 | 45 | 21 / -205.36 | 0 / 0.00 | 5.89 | 0.836 (24) | False | none |
| live | `live-session_breakout-ada-30m` | holdout | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | 68.62 | 68.62 | 0.917 | -35.20 | 45 | 19 / -181.77 | 0 / 0.00 | 7.18 | 0.863 (25) | False | none |
| live | `live-session_breakout-ada-30m` | holdout | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | 65.52 | 65.52 | 0.902 | -35.90 | 45 | 20 / -190.06 | 0 / 0.00 | 7.19 | 0.863 (25) | False | none |
| live | `live-session_breakout-ada-30m` | train | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | -54.03 | -54.03 | 0.183 | -54.03 | 13 | 8 / -76.17 | 0 / 0.00 | 4.04 | 0.828 (3) | False | none |
| live | `live-session_breakout-ada-30m` | train | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | -55.30 | -55.30 | 0.180 | -55.30 | 13 | 9 / -81.38 | 0 / 0.00 | 4.12 | 0.828 (3) | False | none |
| live | `live-session_breakout-ada-30m` | train | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | -54.03 | -54.03 | 0.183 | -54.03 | 13 | 8 / -76.17 | 0 / 0.00 | 4.04 | 0.828 (3) | False | none |
| live | `live-session_breakout-ada-30m` | train | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | -55.30 | -55.30 | 0.180 | -55.30 | 13 | 9 / -81.38 | 0 / 0.00 | 4.12 | 0.828 (3) | False | none |
| live | `live-sma_crossover-eth-30m` | holdout | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | 129.48 | 129.48 | 1.346 | -11.54 | 41 | 6 / -43.51 | 0 / 0.00 | 10.82 | 0.825 (25) | False | none |
| live | `live-sma_crossover-eth-30m` | holdout | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | 134.89 | 134.89 | 1.291 | -13.65 | 42 | 8 / -56.87 | 0 / 0.00 | 10.99 | 0.818 (27) | False | none |
| live | `live-sma_crossover-eth-30m` | holdout | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | 89.46 | 89.46 | 1.007 | -13.95 | 38 | 5 / -37.12 | 0 / 0.00 | 7.57 | 0.735 (25) | False | none |
| live | `live-sma_crossover-eth-30m` | holdout | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | 101.09 | 101.09 | 1.065 | -14.11 | 39 | 6 / -45.05 | 0 / 0.00 | 7.90 | 0.737 (28) | False | none |
| live | `live-sma_crossover-eth-30m` | train | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | 111.29 | 111.29 | 0.814 | -28.60 | 75 | 19 / -142.92 | 0 / 0.00 | 10.83 | 0.801 (40) | False | none |
| live | `live-sma_crossover-eth-30m` | train | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | 155.69 | 155.69 | 0.953 | -24.30 | 72 | 20 / -151.10 | 0 / 0.00 | 14.03 | 0.788 (43) | False | none |
| live | `live-sma_crossover-eth-30m` | train | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | 85.22 | 85.22 | 0.699 | -28.60 | 75 | 19 / -140.21 | 0 / 0.00 | 10.23 | 0.787 (39) | False | none |
| live | `live-sma_crossover-eth-30m` | train | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | 97.82 | 97.82 | 0.701 | -27.15 | 76 | 22 / -163.90 | 0 / 0.00 | 12.01 | 0.767 (43) | False | none |
| live | `live-stoch_rsi-doge-1h` | holdout | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | -35.74 | -35.74 | 0.168 | -52.57 | 6 | 5 / -47.78 | 0 / 0.00 | 5.83 | 0.711 (1) | False | none |
| live | `live-stoch_rsi-doge-1h` | holdout | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | -35.74 | -35.74 | 0.168 | -52.57 | 6 | 5 / -47.78 | 0 / 0.00 | 5.83 | 0.711 (1) | False | none |
| live | `live-stoch_rsi-doge-1h` | holdout | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | -35.74 | -35.74 | 0.168 | -52.57 | 6 | 5 / -47.78 | 0 / 0.00 | 5.83 | 0.711 (1) | False | none |
| live | `live-stoch_rsi-doge-1h` | holdout | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | -35.74 | -35.74 | 0.168 | -52.57 | 6 | 5 / -47.78 | 0 / 0.00 | 5.83 | 0.711 (1) | False | none |
| live | `live-stoch_rsi-doge-1h` | train | `tiered_tp_atr` | 1.5 | 100.00 | 10.00 | 75.0 | -36.70 | -36.70 | 0.541 | -54.30 | 52 | 27 / -237.74 | 0 / 0.00 | 14.40 | 0.845 (20) | False | none |
| live | `live-stoch_rsi-doge-1h` | train | `tiered_tp_atr` | 2.5 | 100.00 | 10.00 | 75.0 | -31.53 | -31.53 | 0.550 | -51.75 | 49 | 27 / -230.40 | 0 / 0.00 | 16.49 | 0.847 (19) | False | none |
| live | `live-stoch_rsi-doge-1h` | train | `tiered_tp_atr_regime` | 1.5 | 100.00 | 10.00 | 75.0 | -46.73 | -46.73 | 0.189 | -56.15 | 10 | 7 / -69.71 | 0 / 0.00 | 22.00 | 0.833 (3) | False | none |
| live | `live-stoch_rsi-doge-1h` | train | `tiered_tp_atr_regime` | 2.5 | 100.00 | 10.00 | 75.0 | -46.73 | -46.73 | 0.189 | -56.15 | 10 | 7 / -69.71 | 0 / 0.00 | 22.00 | 0.833 (3) | False | none |

`* default_off` means a configured directional policy had no certified state and was disabled, matching the backtest/runtime contract.

## Interpretation

- No evaluator family wins across all strategies or both SL levels. Keep current live close refs individually; do not bulk-switch.
- Live XLM liquidates under all tested variants in train and holdout. Demo Liquidity Sweeps liquidates in train for all variants, despite a non-liquidated holdout; treat it as a tail-risk failure.
- Cohort PnL is not pooled: live allocates $100/strategy and demo $1,000, with different per-symbol leverage and some direction/regime differences.
- Explicit close evaluators own close decisions on the current BloFin path. Opposite open signals do not close an existing position unless an evaluator returns a positive close fraction; historic paper `signal` closes are a different/older path.
- Persistent Go/backtest fee defaults remain 0.035%; this report used a regular Level 0 taker override of 0.06%. Reconcile persistent defaults with the desired account/VIP tier before fee-sensitive CLI backtests.

## Implementation and checks

- Backtest fixes: fixed-margin long position tracking; terminal EOD equity includes closing costs; insolvency stops the run and records `liquidation`; `--config` threads market and perps sizing/CB fields.
- Regression test verifies the explicit close owner suppresses opposite open signals while a position is open.
- Targeted tests: 194 passed across fills/close, liquidation, strategy refs, and CLI.
- `git diff --check`: clean. Live/demo configs were not changed; no service was deployed.

## Independent review and missing-market supplement (2026-10-04)

### Correction to the initial market-coverage exclusion

The initial run used a BloFin base URL that returned `Parameter instId error` for `SPCX-USDT` and `USELESS-USDT`. This was an endpoint/configuration mismatch, not evidence that the live markets are unsupported:

- The BloFin instrument endpoint returns both instruments with `instType=SWAP` and `state=live` (`SPCX-USDT`, max leverage 75; `USELESS-USDT`, max leverage 12).
- The v19 demo/live configs contain `order_blocks-SPCX-15m` and `ema_crossover-USELESS-15m` respectively.
- Fetching full history from the same `https://openapi.blofin.com` host configured in both containers succeeds with `store=False`: SPCX had 10,971 15m candles from 2026-06-12 14:00 UTC; USELESS had 12,755 from 2026-05-25. The replay was filtered to fully closed candles through 2026-10-02.

This supersedes the earlier `Unsupported BloFin markets` conclusion. The original 32 cases were omitted because the runner's host environment fell back to the demo API hostname.

### Supplemental 32 evaluator comparisons

The same two evaluators, two SL values, two cohorts, and two folds were rerun for the two missing strategy tuples. Values are net USD / PF / max DD% / closed legs. `Fixed` is `tiered_tp_atr`; `Regime` is `tiered_tp_atr_regime`.

| Cohort | Strategy | Fold | SL | Regime net / PF / DD / N | Fixed net / PF / DD / N |
|---|---|---|---:|---:|---:|
| demo | `order_blocks-SPCX-15m` | train | 1.5 | +196.14 / .653 / -5.63 / 177 | +268.79 / .734 / -4.61 / 166 |
| demo | `order_blocks-SPCX-15m` | train | 2.5 | +219.82 / .660 / -3.69 / 176 | +288.19 / .753 / -4.63 / 161 |
| demo | `order_blocks-SPCX-15m` | holdout | 1.5 | +269.19 / 2.090 / -1.44 / 85 | +315.69 / 2.235 / -1.36 / 89 |
| demo | `order_blocks-SPCX-15m` | holdout | 2.5 | +243.54 / 1.668 / -1.39 / 82 | +308.88 / 1.919 / -1.36 / 89 |
| live | `order_blocks-SPCX-15m` | train | 1.5 | +167.64 / .701 / -46.44 / 179 | +207.13 / .716 / -48.04 / 177 |
| live | `order_blocks-SPCX-15m` | train | 2.5 | +169.95 / .690 / -47.33 / 181 | +214.41 / .700 / -44.72 / 179 |
| live | `order_blocks-SPCX-15m` | holdout | 1.5 | +305.68 / 2.138 / -16.70 / 85 | +357.68 / 2.286 / -16.35 / 89 |
| live | `order_blocks-SPCX-15m` | holdout | 2.5 | +273.28 / 1.757 / -15.96 / 82 | +348.14 / 2.005 / -16.70 / 89 |
| demo | `ema_crossover-USELESS-15m` | train | 1.5 | +286.60 / .667 / -4.63 / 241 | +247.63 / .630 / -5.05 / 238 |
| demo | `ema_crossover-USELESS-15m` | train | 2.5 | +281.75 / .653 / -5.13 / 231 | +261.12 / .642 / -6.72 / 223 |
| demo | `ema_crossover-USELESS-15m` | holdout | 1.5 | +293.25 / 1.348 / -3.89 / 103 | +416.74 / 1.661 / -3.54 / 111 |
| demo | `ema_crossover-USELESS-15m` | holdout | 2.5 | +288.32 / 1.324 / -3.89 / 101 | +420.96 / 1.693 / -3.52 / 108 |
| live | `ema_crossover-USELESS-15m` | train | 1.5 | +386.49 / .670 / -16.61 / 239 | +365.77 / .637 / -16.61 / 236 |
| live | `ema_crossover-USELESS-15m` | train | 2.5 | +426.51 / .798 / -20.92 / 208 | +428.72 / .790 / -20.92 / 202 |
| live | `ema_crossover-USELESS-15m` | holdout | 1.5 | +291.85 / 1.658 / -10.60 / 103 | +363.55 / 1.887 / -9.14 / 110 |
| live | `ema_crossover-USELESS-15m` | holdout | 2.5 | +281.54 / 1.557 / -11.84 / 92 | +355.85 / 1.948 / -9.78 / 95 |

The additional 32 cases completed with zero errors. An independent replay of the live SMA Crossover / ETH 30m / holdout reference produced the same 39 legs and close metrics within the pre-set check tolerance: $99.54 / PF 1.090 / DD -14.18%, versus the original $101.09 / PF 1.065 / DD -14.11%. This small residual is retained as a reproducibility difference; the supplemental table is not presented as bit-identical to the original run.

### Updated evaluator assessment for all 11 live strategies

| Live strategy | Updated decision from the two-family bakeoff |
|---|---|
| `consolidation_range-XLM-15m` | Both evaluator families liquidate in the live train and holdout at both SL settings. Do not select a TP evaluator from these runs; solve risk/sizing/liquidation first. In demo, the winner changes with SL (regime at 2.5, fixed narrowly at 1.5). |
| `sma_crossover-ETH-30m` | **Strongest fixed-ATR candidate.** `tiered_tp_atr` beats the regime evaluator in live and demo, train and holdout, at both SL values; PF is also consistently higher. Trial fixed ATR in paper/canary before live. |
| `stoch_rsi-DOGE-1h` | Fixed ATR is clearly better in demo and less bad in live train, but live holdout is negative and identical for both (six legs). Paper-test fixed ATR; no live switch yet. |
| `heikin_ashi_ema-ETH-30m` | Keep regime evaluator for live: it wins the live holdout at both SL values and materially lowers DD. Demo prefers fixed ATR, so maintain the cohort distinction rather than pooling results. |
| `order_blocks-SPCX-15m` | **Strong fixed-ATR candidate.** Fixed ATR wins PnL and PF in all eight demo/live × train/holdout × SL comparisons. Live train DD remains roughly 45–48%, so use paper/canary and respect the global portfolio cap. |
| `liquidity_sweeps-ETH-30m` | Fixed ATR wins both holdouts (live and demo), but regime wins live training; keep as a rolling-paper candidate, not a settled live switch. |
| `session_breakout-ADA-30m` | Keep regime evaluator. It beats fixed ATR in live and demo holdouts, often by a wide margin, and is better in demo training. |
| `regime_adaptive-SPX-15m` | Keep regime evaluator for live: it is less negative in the live holdout at both SL values and has a better drawdown profile. Demo has an SL-dependent mixed result. |
| `heikin_ashi_ema-HYPE-30m` | Keep regime evaluator. It wins net PnL in train/holdout across both cohorts; fixed ATR has slightly shallower DD in some folds. |
| `parabolic_sar-ETH-15m` | Fixed ATR produces more net PnL in most folds, but live holdout DD worsens materially (to -27.83% at SL 2.5 versus -16.08% for regime). With a 20% portfolio cap, retain regime unless a portfolio-level replay proves the extra gross return survives. |
| `ema_crossover-USELESS-15m` | Fixed ATR wins both holdouts in demo and live, but regime wins demo training and slightly wins live training at SL 1.5; SL 2.5 is nearly tied in live training. Promising paper candidate, not yet a stable live switch. |

Across the now-complete 11-strategy holdout comparison, fixed ATR is especially compelling for SMA ETH and Order Blocks SPCX; regime ATR remains strongest for Session Breakout, HYPE Heikin Ashi, live ETH Heikin Ashi, and SPX Regime Adaptive. The liquidating XLM runs, the fold reversal on USELESS/LIQ Sweeps, and drawdowns above the global 20% portfolio limit prevent a blanket switch.

### Implementation follow-up found during this review

- A source inspection found duplicate `argparse` registrations for `--position-regime-pending-label` and `--position-regime-pending-count` in the BloFin signal-check parser. A real `check_blofin.py --help` invocation failed with `argparse.ArgumentError`; the duplicate options were removed and a subprocess smoke test was added.
- After that fix, the CLI smoke and 175 targeted Python tests passed. The second-session report records 194 tests; this review could not rerun Go tests because no Go executable is installed on the host. No service was deployed.
- Fee/slippage model: the supplemental replay used the same 0.06% taker and 5 bp assumptions. The report still does not state funding accrual; verify funding inclusion before describing these values as fully net perps profit.
- The new explicit close-owner path suppresses opposite-signal closes while a position is open unless the evaluator returns a positive fraction. These backtests therefore evaluate the new close-owner semantics, not historical paper rows closed by the older signal path.

---

## Corrected full 12×11 evaluator matrix — 2026-10-05

### Accounting corrections

The historical PnL conclusions in the sections above must not be used for evaluator selection. Two fixed-margin paths were incorrect:

1. Partial exits returned a fraction of margin to cash without reducing `margin_locked` (or the short-side `_notional`) by the same fraction. The returned collateral was therefore counted again as locked collateral.
2. The `ohlc_walk` intrabar-stop branch bypassed the margin-aware close routine: long exits credited gross proceeds and short exits debited gross buyback cost. It also left the margin/notional state stale. Intrabar stops now route through `_book_close`, sharing the same margin-aware accounting as other full exits.

Regression coverage in `backtest/tests/test_backtester_fills.py` now verifies both long and short fixed-margin cases:

- With $10 margin, 2× leverage, and partial ATR exits at +1 ATR/+2 ATR, $1,000 starting capital ends at $1,003 (the pre-fix implementation returned $1,008).
- With $10 margin, 2× leverage, and a 1 ATR intrabar stop, $1,000 ends at $998 for either long or short.

### Scope and method

- Live config v19, 11 live strategies, all 12 BloFin-perps-supported close evaluators; one evaluator at a time. The live config, services, and trading state were not changed.
- The v19 open strategy, registry defaults, direction, regime policy/windows, sizing, leverage, and per-strategy max drawdown were retained. Explicit open params are empty in this config; args contain only the strategy/symbol/timeframe and `--mode live`.
- 264 runs: 132 strategy/evaluator pairs, each replayed independently on train (through 2026-08-23) and holdout (from 2026-08-24). Holdout signals before the split were disabled while earlier candles remained available for indicator warm-up.
- Closed BloFin candles were fetched from `https://openapi.blofin.com`; `store=False`, `save=False`. No OHLCV/results persistence was enabled. Commission was 0.06% taker per fill and slippage 5 bp per fill.
- Each live strategy starts at $100 with $10 margin/trade. Leverage is 75× except USELESS at 12×. Per-strategy drawdown cap is 50%; the global portfolio 20% cap is not modelled in these isolated runs. Funding accrual was not applied.
- This is a one-point screening, not parameter optimization. `tiered_tp_atr_regime` with `sl_atr_mult=2.5` and `use_defaults=true` is the per-strategy control. TP-only candidates use an independent 2.5 ATR stop; stop evaluators own their stop; the dynamic regime evaluator owns a unified 2.5 ATR per-regime stop. `time_stop` is 24 hours (96/48/24 bars for 15m/30m/1h), `zscore_target` is lookback 20/target 2.0, `avwap_stop` buffer is 0.25 ATR, ATR stop is 2.5 entry ATR, scalar ratchet starts at 2.5 ATR, and regime ratchet uses the default regime trailing block.

### BloFin candle coverage

All nine symbol/timeframe series were continuous over the included interval (zero gaps); each series was trimmed to its last fully closed candle on 2026-10-02.

| Symbol | Timeframe | Candles | First | Last closed | Gaps |
|---|---:|---:|---|---|---:|
| ADA | 30m | 6,287 | 2026-05-25 00:00 | 2026-10-02 23:00 | 0 |
| DOGE | 1h | 3,143 | 2026-05-25 00:00 | 2026-10-02 22:00 | 0 |
| ETH | 15m | 12,575 | 2026-05-25 00:00 | 2026-10-02 23:30 | 0 |
| ETH | 30m | 6,287 | 2026-05-25 00:00 | 2026-10-02 23:00 | 0 |
| HYPE | 30m | 6,287 | 2026-05-25 00:00 | 2026-10-02 23:00 | 0 |
| SPCX | 15m | 10,791 | 2026-06-12 14:00 | 2026-10-02 23:30 | 0 |
| SPX | 15m | 12,575 | 2026-05-25 00:00 | 2026-10-02 23:30 | 0 |
| USELESS | 15m | 12,575 | 2026-05-25 00:00 | 2026-10-02 23:30 | 0 |
| XLM | 15m | 12,575 | 2026-05-25 00:00 | 2026-10-02 23:30 | 0 |

The prior supplemental note's SPCX count of 10,971 was incorrect for the stated first/last timestamps; 10,791 is the exact 15m interval count.

### Run integrity and required context

- Constructor preflight: 132/132 combinations accepted.
- Backtests: 264/264 completed; zero exceptions. No corrected run was liquidated.
- Cash/ledger reconciliation: `final_capital - initial_capital` equals summed closed-trade PnL plus funding to within cent-rounding; maximum absolute delta was $0.06, zero cases exceeded the per-run rounding tolerance.
- Required-context checks found only two `noop:missing_zscore` calls (both during train warm-up); no missing AVWAP, ATR, position regime, or ratchet context was observed.
- Control replay `live-sma_crossover-eth-30m`, `tiered_tp_atr_regime`, holdout: +$0.13 net, PF 1.002, max DD −27.12%, 39 trades.

### Corrected current-control results by live strategy

Values are net USD / profit factor / max drawdown % / closed trades; each row starts at $100. No returns are pooled across strategies.

| Live strategy | Train | Holdout |
|---|---|---|
| `live-stoch_rsi-doge-1h` | −60.7 / 0.08 / −63.5 / 6 | −48.6 / 0.14 / −58.8 / 6 |
| `live-consolidation_range-xlm-15m` | −52.1 / 0.52 / −55.7 / 24 | −51.5 / 0.67 / −55.4 / 50 |
| `live-sma_crossover-eth-30m` | −45.3 / 0.58 / −52.0 / 36 | +0.1 / 1.00 / −27.1 / 39 |
| `live-session_breakout-ada-30m` | −52.1 / 0.22 / −54.2 / 13 | −7.6 / 0.95 / −50.2 / 41 |
| `live-regime_adaptive-spx-15m` | −43.1 / 0.33 / −52.6 / 13 | −18.7 / 0.85 / −51.9 / 26 |
| `live-heikin_ashi_ema-hype-30m` | −51.2 / 0.40 / −54.5 / 13 | −46.6 / 0.33 / −50.0 / 11 |
| `live-order_blocks-spcx-15m` | −5.7 / 0.97 / −54.3 / 68 | +83.2 / 1.74 / −19.9 / 95 |
| `live-parabolic_sar-eth-15m` | −43.2 / 0.30 / −51.3 / 21 | −40.9 / 0.65 / −51.0 / 52 |
| `live-ema_crossover-useless-15m` | −47.3 / 0.78 / −50.8 / 173 | +51.1 / 1.50 / −18.0 / 95 |
| `live-heikin_ashi_ema-eth-30m` | −49.4 / 0.19 / −52.4 / 18 | +8.0 / 1.04 / −45.9 / 82 |
| `live-liquidity_sweeps-eth-30m` | −53.6 / 0.35 / −53.6 / 16 | +3.5 / 1.06 / −29.1 / 22 |

### Pairwise evaluator comparison with current live control

A win/loss/tie is the candidate's net USD versus `tiered_tp_atr_regime` for the same strategy and fold; ties are within $0.01. “Both folds” counts strategies where the candidate beats the control in both train and holdout. “DD better” counts holdout pairs with a less-negative max drawdown. There were no liquidated pairs in this corrected screen.

| Evaluator | Holdout W/L/T | Train W/L/T | Net wins in both folds | Holdout DD better |
|---|---:|---:|---:|---:|
| `atr_stop` | 5 / 6 / 0 | 9 / 2 / 0 | 4 | 2 |
| `avwap_stop` | 1 / 10 / 0 | 5 / 6 / 0 | 1 | 1 |
| `tiered_tp_atr` | 6 / 4 / 1 | 4 / 6 / 1 | 1 | 6 |
| `tiered_tp_atr_live` | 5 / 5 / 1 | 4 / 7 / 0 | 2 | 4 |
| `tiered_tp_atr_live_regime` | 5 / 6 / 0 | 5 / 6 / 0 | 4 | 4 |
| `tiered_tp_atr_live_regime_dynamic` | 5 / 6 / 0 | 5 / 6 / 0 | 3 | 3 |
| `tiered_tp_pct` | 6 / 5 / 0 | 8 / 3 / 0 | 4 | 5 |
| `time_stop` | 5 / 6 / 0 | 6 / 5 / 0 | 3 | 1 |
| `trailing_tp_ratchet` | 5 / 6 / 0 | 5 / 6 / 0 | 3 | 5 |
| `trailing_tp_ratchet_regime` | 4 / 7 / 0 | 3 / 8 / 0 | 1 | 5 |
| `zscore_target` | 4 / 7 / 0 | 5 / 6 / 0 | 2 | 2 |

No evaluator dominates across all 11 live strategies and both folds. The corrected screen does not justify a blanket live switch; retain per-strategy close refs pending broader rolling walk-forward and portfolio-level replay, including the global 20% drawdown cap and funding where applicable.

### Verification

- `test_backtester_fills.py`, `test_backtester_close_strategies.py`, `test_metrics_liquidation.py`, `test_backtester_position_avwap.py`: 109 passed.
- `git diff --check`: clean.
- Go scheduler suite ran in a temporary `golang:1.26-alpine` container (Go 1.26.8) with the repo mounted read-only and module files unchanged. Its sole reported failure was `TestMergePaperInstance`: `scripts/update_helpers.sh` apply step raised `IndexError: list index out of range` in its Python path selector; no other Go test reported failure.
- No live config, service, or exchange state was changed.
