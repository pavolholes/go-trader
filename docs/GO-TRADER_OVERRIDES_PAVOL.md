# GO-TRADER OVERRIDES PAVOL

Pavol fork overrides vs upstream `richkuo/go-trader`. MUST be preserved across
upstream merges. Base: v0.106.0 (`0064f5d`, merged 2026-10-01; previous base
v0.104.0). This file is referenced from `CLAUDE.md` (root `AGENTS.md` is a
symlink to it).

## 1. New files (upstream does not have them — keep on merge)

| File | Purpose |
|---|---|
| `platforms/blofin/spot_adapter.py` | `BloFinSpotExchangeAdapter` — spot market data via `/api/v1/spot/market/*` (candles 9→6 fields, tickers, instruments, price); cursor-paged candles above 1440. Phase 1 = market data only |
| `shared_scripts/export_spot_top.py` | USDT-volume ranking (`volCurrency24h × last`) of spot symbols per category; outputs phase-1 symbol set |
| `scheduler/blofin_spot_test.go` | `TestSpotEffectiveDirectionIsLongOnly` — spot is long-only |
| `docs/GO-TRADER_OVERRIDES_PAVOL.md` | This file |
| `shared_strategies/open/ob_touch.py` | `ob_touch_core` SMC order-block touch entries with 1H overlap confirmation (TradingView Institutional Level entry port; section 10) |
| `shared_strategies/open/test_ob_touch.py` | 21 behavior tests for `ob_touch` (zone math, first-touch, mitigation, HTF gate, no-lookahead) |
| `shared_tools/ob_touch_data.py` | Conservative LTF forming-candle trim for `ob_touch` MTF signal checks; backtests unchanged |
| `shared_tools/test_ob_touch_data.py` | Closed-LTF-frame behavior tests |
| `platforms/blofin/test_ohlcv_pagination.py` | Perps/spot multi-page history tests (1440 response cap, cursor overlap dedupe) |
| `platforms/topstep/test_yahoo_retry.py` | Yahoo transient retry, outage fail-closed, and 60d/7d interval period policy tests |
| `shared_scripts/test_check_blofin_inst_type.py` | Regression tests for equals- and space-separated spot/swap registry selection |
| `shared_scripts/test_check_ob_touch_history_floor.py` | Regression tests for 5m=2000 and 15m=1200 LTF fetch floors in BloFin/TopStep checks |
| `scheduler/config_pavol_test.go` | Fork config tests (perps leverage, spot rejection, Discord channel env) — upstream #1597 deleted the original file content, ours live here |
| `scheduler/risk_pavol_test.go` | Fork risk tests (BloFin force-close, concise warning, spot multiplier) — same reason |
| `scheduler/portfolio_pavol_test.go` | Fork spot valuation test — same reason |
| `scheduler/main_pavol_test.go` | Fork routing tests (channel-only trade alerts incl. BloFin, circuit-breaker) — same reason |

## 2. BloFin perps (platform `blofin`, prefix `bl-`)

| File | Override |
|---|---|
| `platforms/blofin/adapter.py` | HMAC signing (ms timestamp, field order, base64); copy-trading routing via `BLOFIN_TRADE_ACCOUNT=copy` (`/api/v1/copytrading/*` for place/close/positions/leverage); balance response shape fix; OHLCV cursor pagination for requests above the 1440-candle endpoint cap |
| `scheduler/blofin_exec.go` | `blofinIsLive`, `blofinSymbol`, `runBloFinCheck`, `executeBloFinResult`, copy-trading dispatch |
| `scheduler/blofin_close.go` | Force-close / circuit-breaker close via `close_blofin_position.py` |
| `scheduler/blofin_marks.go` | `fetchBloFinPerpsMids` via `/api/v1/market/tickers?instType=SWAP` |
| `shared_scripts/check_blofin.py` | `--inst-type {swap,spot}` (default `swap`); spot branch uses `spot_adapter`; `--atr-method`; regime/HTF passthrough; `--htf-timeframe/--htf-limit` (deep HTF fetch injected as `htf_df` for `ob_touch`, fail-closed); `_detect_inst_type` accepts `--inst-type spot` and `--inst-type=swap`; `ob_touch` LTF floor 2000 bars on 5m / 1200 on 15m; trims trailing possibly-forming LTF candle; accepts `--position-risk-anchor-price` (upstream probe parity, ignored like Topstep) |
| `shared_scripts/check_topstep.py` | `--htf-timeframe/--htf-limit` (same HTF injection; paper serves yfinance NQ=F); yahoo fetch has bounded retry (3x/5s) for flaky `fc.yahoo.com`; 5m/15m queries use 60d Yahoo history (1m stays 7d); `ob_touch` LTF floors 2000/1200; trims trailing possibly-forming LTF candle |
| `shared_scripts/close_blofin_position.py` | Emergency close via `adapter.market_close()` (swap only) |
| `shared_scripts/fetch_blofin_positions.py` | Open positions fetch (swap) |
| `shared_scripts/fetch_blofin_balance.py` | USDT equity fetch (futures) |
| `shared_scripts/check_price.py` | **BloFin spot fallback**: symbols missing from Binance.US are filled from `/api/v1/spot/market/tickers` (host cannot reach `api.binance.us`) |
| `scheduler/fees.go` | `BloFinTakerFeePct`, `CalculatePlatformSpotFee("blofin")` |
| `scheduler/risk.go` | BloFin constants; perps margin DD inputs |
| `scheduler/regime_atr.go` | `sl_atr_mult` close param allowed |
| `scheduler/config.go` | `regime_directional_policy` allowed on BloFin perps; `sl_atr_mult` validation |
| `scheduler/portfolio.go`, `db.go`, `hyperliquid_balance.go`, `manual.go`, `deribit.go` | `Position.RealizedPnLAccum` — `closed_positions.realized_pnl` sums ALL legs, not final leg |

## 3. BloFin spot (platform `blofin_spot`, prefix `bls-`, type `spot`)

| File | Override |
|---|---|
| `scheduler/config.go` | `bls-` ID prefix → `Platform = "blofin_spot"` (before `bl-` rule); `type=spot` + `platform=blofin_spot` validation; direction/leverage validated perps-only |
| `scheduler/main.go` | Dispatch: `sc.Platform == "blofin" \|\| "blofin_spot"` (posCtx + `runBloFinCheck` branch); spot flows through generic `case "spot":` → `runSpotCheck` + `executeSpotResult` |
| `shared_tools/data_fetcher.py` | `fetch_ohlcv_blofin()` (perps, generic `/market/candles` — legacy); `fetch_ohlcv_blofin_spot()` (spot endpoint); routing `blofin` / `blofin_spot` BEFORE `get_exchange()` |
| `shared_scripts/check_strategy.py` | `--exchange` flag (default `binanceus`), threaded into all 3 `fetch_ohlcv` calls |
| Config entry shape | `type=spot`, NO `direction`/`leverage`/`margin_per_trade_usd` (Go rejects them for spot); `capital` = paper capital (100); close `tiered_tp_atr_regime` + `sl_atr_mult 1.5` |

## 4. UI customizations (`scheduler/static/ui/`, `scheduler/ui_server.go`, `scheduler/server.go`)

| Area | Override |
|---|---|
| Overview table | Restored summary view (3rd mode, default); column filters; `Futures`/`Spot` master filter (`row.type`, needs `Type` in JSON); sortable `DD %` (numeric) |
| Overview columns | `Type` field in `UIStrategyOverview` JSON; `DD %` = **equity DD** `(peak-value)/peak` (NOT margin DD from `RiskState`); colored `Sharpe` (≥2 great, ≥1 good, <0 bad), `WinRate` (≥60/≥50/<35), `DD` (>10 bad, >5 mid) |
| Overview perf | `fetchLiveMarkPrices()` ONCE per request + **5-min response cache** (`overviewCache`, `server.go`); detail view loads sparkline for active strategy only (not all 1526) |
| Summary view | Cards + Top/Bottom/WinRate/Active/ByType/BySymbol/Strategy×Symbol/Strategy×Timeframe; `Dead strategies` panel moved here |
| Table view | `Trade diagnostics` panel (back here); Cashflow/Correlation/Close evaluators/Add strategy stay |
| Misc | `DASHBOARD_ACCENT` via `styles.css`; `/` → `/dashboard` redirect; dark-mode CSS vars; responsive chart |

## 5. Discord / notifier

| File | Override |
|---|---|
| `scheduler/discord.go`, `notifier.go` | `DISCORD_TRADES_CHANNEL_ID` + `DISCORD_DAILY_SUMMARY_CHANNEL_ID` env (fallback `default`); paper suppression (`blofin-paper: ""` = no spam) |
| `scheduler/agent_info.go` | New env vars surfaced |

## 6. CI / tests (upstream #1597 policy: no unit tests except money math,
parity, migrations, rare venue states — verify by running real binaries)

- `check_strategy.py --exchange`, `data_fetcher` blofin branches: `py_compile` covered.
- `blofin_spot_test.go`, `portfolio_closedpnl_gross_test.go`, `hurst_gate_wiring_test.go`, `discord_test.go`, `ui_accent_test.go` (kept as-is on merge — upstream deleted these files, we restore ours).
- `config_pavol_test.go`, `risk_pavol_test.go`, `portfolio_pavol_test.go`, `main_pavol_test.go` (fork tests moved here — upstream trimmed the original files beyond recognition; `test_budget_baseline.json` points at the new paths).
- `TestUpdateShell*` skipped (env-specific `safe.directory`).
- `gofmt` clean required (CI `Format check` gates).
- `CLAUDE.md` must stay under the 17000-byte CI ceiling (upstream sits at 16992 — any fork sentence needs a wording shorten elsewhere).

## 7. Merge checklist (upstream → fork)

1. `git fetch upstream`; merge `upstream/main` (or version tag) into `main`.
2. Expected conflict zones: `scheduler/config.go`, `main.go`, `db.go`, `discord*.go`, `notifier*.go`, `ui_server.go`, `ui_summary.go`, `static/ui/*`, `CLAUDE.md`/`SKILL.md`, `README.md`, backtester + close evaluators (upstream refactors fast here).
3. Keep ALL files from section 1 (new files never conflict — verify they survive).
4. Re-apply sections 2–5 and 10 item by item; upstream refactors (e.g. paper-source partitions, notifier rewrites, `_book_close`, `regime_ladder_for`, `applyNotifierEnvOverrides`) may relocate the code — search by function name, not line number. `db.go` rule: UNION of position columns (fork `realized_pnl_accum` + upstream additions) in schema, migrations, INSERT/SELECT/Scan.
5. `gofmt -l`, `go build ./...`, `go vet`, `go test`, `py_compile` on Python touchpoints.
6. Push → CI green → rebuild demo image → verify `/api/strategies/overview` + dashboard.
7. Live (`go-trader-live`) rebuild only after demo proves stable.


## 8. Learnings

Prevádzkové poznatky: LEARNINGS_PAVOL.md (rovnaký adresár).


## 9. Sidebar odstraneny

Lavy stlpec (zoznam strategii + search + sparklines) je odstraneny z Table aj Summary view: index.html (aside, toggle, backdrop), app.js (renderStrategies/loadSparklines no-op, ziadny search listener), styles.css (.shell na 1 stlpec). Dovod: nepouzivane + 1300+ sparkline requestov brzdilo nacitanie. Pri upstream mergi neobnovovat.

## 10. Custom strategy ob_touch (SMC order-block touch + 1H overlap)

| File | Override |
|---|---|
| `shared_strategies/open/ob_touch.py`, `test_ob_touch.py` | New files (section 1) keep on merge |
| `shared_strategies/open/registry.py` | `@register("ob_touch", ...)` block after `mtf_confluence_strategy`; `PLATFORM_ORDER` spot+futures entries before `"hold"`; defaults mirror the TradingView screenshot (internal 5, swing 50, 7 levels, Absolute/Precise/Previous, touch `wick`, spot `allow_short: False`, futures `True`); description documents the HTF factor guide (5m x 12 = 1H, 15m x 4 = 1H, 5m x 3 = 15m, 1 = single-TF) |
| `backtest/optimizer.py` | `DEFAULT_PARAM_RANGES["ob_touch"]` (CI param-ranges test fails without it) |
| `scheduler/init.go` | `knownShortNames["ob_touch"]="obt"` (`"ob"` is taken); `bidirectionalPerpsStrategies["ob_touch"]=true` |
| `shared_scripts/check_blofin.py`, `check_topstep.py` | `--htf-timeframe/--htf-limit` + `htf_df` injection gated on the effective open strategy being `ob_touch` |
| first-touch semantics | Pine parity kept: every fresh wick entry into the same zone fires again (crossunder semantics); pinned by `test_reentry_after_exit_fires_again`, no consumed-zone tracking |

Demo-side (outside this repo, survives merges by location): `dockge/go-trader-demo/compose.yaml` installs `yfinance` (Topstep paper); `go-trader-demo/config/config.json` paper set = 64x perps (`bl-ob_touch-*`, incl. `QQQ` Nasdaq proxy, per-symbol max leverage, `direction: both`) + 24x spot (`bls-ob_touch-*`, long-only) + 4x Topstep (`ts-ob_touch-{nq,mnq}-{5m,15m}`, capital 25000/3000, `maxContracts: 1`); all entries carry `open_strategy.params.htf_factor` 12 for 5m or 4 for 15m. Dedicated `go-trader-ob-touch` paper service uses port 8102, `config-ob-touch.json`, `data/ob_touch-state.db`, and log_dir `data/ob_touch-logs`; main demo keeps the other 1345 strategies. Backup: `config.json.bak-ob_touch-20260930`. `direction` is INVALID on `type: futures` (config load fails) never add it there.
