# BloFin Copy Trading Cross-Margin Risk — Audit and Fix Plan

**Status (updated 2026-09-28):** The approved BloFin Copy `totalEquity` risk source, persisted baseline, entry-only 20% latch, three-reading auto-rearm below 16%, and channel-only notifications are implemented and deployed. The original trusted baseline remains **$375.460214 USDT**; the stored high-water peak is **$381.154877 USDT**, with live `/status` drawdown **7.83%** and no kill latch. Twelve confirmed Copy close fills have been reconciled into the live DB; the DB passes integrity check and has no open positions, matching the last exchange snapshot. The BloFin mark-price path is deployed and verified in `/status`. Bot-managed close/stop evaluators remain active during the entry hold; exchange-native Copy TPSL orders are not currently verified.

**Currently deployed:** portfolio warning threshold is configured at 80% of the 20% model limit (16%), with a simpler message and 2pp escalation. Those warning inputs are still model-derived and must not be represented as BloFin account-equity or cross-margin measurements.

## Executive summary

The current portfolio warning uses internal virtual strategy-book value and estimated per-position margin. Its labels sound like BloFin account equity and cross margin, but they are not those exchange metrics. The global kill-switch is likewise based on the virtual strategy book when equity is available, and the live close planner is not wired to BloFin Copy Trading. A recent exchange-confirmed SPCX partial close was also skipped by the live ledger.

## Audit snapshot (2026-09-27)

- BloFin Copy Trading asset balance: **$396.447 USDT**.
- BloFin Copy Trading perps `totalEquity`: **$396.598**, available **$346.164**.
- At the same audit, Go `/status` reported internal `TotalPV` **$1,223.04**, peak **$1,253.32**, equity drawdown **2.28%**, and modeled perps-margin drawdown **7.09%**. These are not account equity/margin fields.
- BloFin returned 6 open positions before the close sequences and **4 open positions / 0 open orders** afterward.
- SPCX parent position `16931262` had 504 original contracts. Three strategy SELL close orders filled: `16967850` 201 contracts (PnL +$0.1809, fee $0.17948898), `16968008` 201 (PnL +$0.201, fee $0.17950104), and `16968123` 102 (PnL +$0.102, fee $0.09109008). Total 504 closed; gross PnL +$0.4839 and fees $0.45008010. BloFin position history confirms the parent position is closed.
- USELESS parent position `16924903` also closed in two fills: `16968579` sold 16 contracts (PnL +$0.8694, fee $0.02823876) and `16968847` sold 25 (PnL +$2.8725, fee $0.0450315). Gross PnL +$3.7419, fees $0.07327026.
- Both exits were absent from the original ledger: the Bot logged “without fill” and kept stale quantities, then retried partial sells until BloFin was flat. After full Copy-history reconciliation the DB has 81 trades, 55 close fills, 22 closed positions, 4 open positions, and realized gross PnL +$144.60736 (`integrity=ok`).
- Current exchange-open order IDs are SPX `16944011`, HYPE `16927677` (41 remaining from 81), DOGE `16923379`, and XLM `16921894`; the DB now matches these. No SPX order was submitted in the inspected 30-minute window.
- Discord config has no `discord.owner_id`; container environment has no `DISCORD_OWNER_ID`. Therefore owner DMs are silently skipped. Channel warnings still send.
- Warning events at 09:38:13Z and 09:38:36Z were two separate margin-band events, not a channel/DM duplicate. The warning flag resets when metrics leave the band, so crossing back over the threshold can notify immediately.

## Confirmed design gaps

### A. “Equity” is a virtual strategy-book total

`measureScopeCycleRisk` calls `computeSubsetPortfolioValue`. BloFin is not supported by the shared-wallet balance-fetcher registry, so the live total is the sum of strategy `PortfolioValue` values (virtual cash plus model mark-to-market PnL). The peak around `$1,253` is the high-water mark of that aggregate, not the BloFin account balance near `$396`.

The 20% drawdown calculation is therefore against the virtual book. At a `$1,253` peak, its trigger corresponds to a model total near `$1,002`, not a 20% decline from BloFin's cross-account equity.

### B. “Perps margin” is an estimate, not Cross margin

`AggregatePerpsMarginInputs` sums each open strategy position's notional divided by that strategy's configured leverage. Its loss numerator sums only negative per-position unrealized PnL; profitable positions do not offset losing ones. The ratio is a model estimate, not BloFin's account-level Cross margin usage, account margin ratio, or net unrealized PnL.

### C. The 20% latch is not a BloFin automatic close

- With a trusted equity reading, the latch fires on virtual strategy-book equity drawdown. The modeled margin ratio is a warning/input; it does not replace equity as the latch source.
- `CheckPortfolioRisk` latches and returns `allowed=false`; it does not itself close an exchange position.
- `main.go` does not populate the BloFin closer/fetcher fields in `KillSwitchCloseInputs`; `planKillSwitchClose` has no BloFin close implementation.
- `forceCloseAllPositions` deliberately preserves a live BloFin position unless an exchange fill is confirmed. The global kill path can therefore leave BloFin positions open while the risk latch is set.
- The close plan defaults `OnChainConfirmedFlat` to true and does not verify BloFin. `CanAutoResetWithoutOwner` has no BloFin-flat evidence to check, while `AutoResetConfirmedFlatKillSwitch` only clears/re-baselines the latch. With `DISCORD_OWNER_ID` unset, the ownerless auto-reset path can clear the latch while BloFin Copy positions remain open.
- When a per-strategy circuit breaker is active, `CheckRisk` blocks the strategy. The close-management exemption currently permits continued management for Hyperliquid only, not BloFin. Thus a BloFin software close signal may not be evaluated during the breaker.
- The BloFin Copy order path does not attach SL/TP fields when opening positions. The pending TPSL/trigger-order reads were empty at audit time. Configured `tiered_tp_atr_regime` exits should be treated as bot-managed logic, not assumed exchange-resting stops.

### D. Fill-to-ledger reconciliation is still incomplete

Copy order-history IDs `16967850`, `16968008`, and `16968123` record the SPCX fills; position-detail close ID `7363438` is a different ID namespace. USELESS had the same missed-fill pattern. A stable `clientOrderId` is now submitted and the fill poller can match it or the canonical Copy-history ID. Python regression tests cover an acknowledgement close-detail ID differing from the parent history ID. No live close has occurred after this change yet, so end-to-end exchange verification is still pending.

### E. Discord owner DM is not configured; threshold can chatter

- `SendOwnerDM` is a no-op when owner ID is blank. Configure `DISCORD_OWNER_ID` or `discord.owner_id` and verify the DM route end-to-end.
- `DISCORD_TRADES_CHANNEL_ID` is configured; the failed fill path returned zero recorded trades, so `sendTradeAlerts` had nothing to publish. One aggregated recovery summary for the five missed close fills was posted after ledger reconciliation. Owner DMs remain unconfigured.
- The 16% threshold is currently applied to model equity or modeled margin. The 2pp escalation and six-hour reminder do not suppress immediate notifications after the risk state leaves and re-enters the band; add a re-entry cooldown/hysteresis.

## Proposed target design

1. **Account-of-record:** Use BloFin Copy Trading account `totalEquity` for actual account drawdown; keep wallet balance, available balance, and unrealized PnL separately named. Establish and persist a new exchange-equity peak baseline; never compare the existing virtual `$1,253` peak to the account's `$396` balance.
2. **Cross-margin measurement:** Prefer exchange-reported account/position fields. If true used margin is unavailable, label any notional/leverage figure as a model estimate and do not present it as BloFin Cross margin or as a liquidation threshold.
3. **Kill policy — user selected entry-halt only:** at a confirmed actual account-equity breach, latch and block new/increasing exposure. Do not automatically flatten existing Copy positions; continue risk-reducing exits and partial closes.
4. **Close-only handling:** A portfolio/strategy breaker must reject new entries and scale-ins but permit risk-reducing closes, partial take-profits, stop maintenance, and reconciliation.
5. **BloFin close confirmation:** Implement parent-position/order-ID correlation, stable `clientOrderId` where supported, exact fill/fee booking, retries that cannot over-close, and exchange-confirmed flat checks. Never clear virtual position state from a mark price alone.
6. **Protection:** Current Copy positions use the configured bot-managed close evaluator; keep it running during the entry hold and circuit-breaker management mode. Do not claim exchange-native SL/TP protection until active Copy TPSL orders are verified. Add a visible protection-status check in a follow-up.
7. **Notifications:** Name `BloFin Copy Trading totalEquity`, state that the latch holds entries but leaves existing positions open, and report the 3-reading re-arm rule. Send informational updates to configured channels only; suppress trade-alert DMs and ignore `notify_dm` for delivery routing.

## Implementation phases

### P0 — Reconcile current Copy ledger (completed 2026-09-27)

1. Paused automation, took a consistent SQLite backup, and staged the complete Copy Trading history.
2. Rebuilt SPCX `16931262` and USELESS `16924903` as closed, booking all five confirmed close fills, fees, and realized PnL.
3. Installed after integrity/count validation, preserved consistent/raw/WAL/SHM backups, restarted, and confirmed the four remaining exchange positions match DB.

### P1 — Make fill correlation reliable

Stable client-order-ID submission and Copy-history lookup are implemented and deployed. Regression tests cover a close-detail acknowledgement ID differing from the Copy-history parent ID, unfilled acknowledgements, and live partial-close trade-alert routing. Verify against the next real fill; do not use a test market order.

### P2 — Base risk on the actual Copy account

Fetch and persist Copy account `totalEquity`, establish a new source-tagged peak baseline on the first trusted snapshot, replace the virtual live-book value for the account risk gate, and omit model-estimated margin from Copy account warnings.

### P3 — Implement the chosen kill/exit policy

At 20% account drawdown, latch new/increasing exposure only. Never invoke the global close planner for this account latch. Keep position-reducing signals and bot-managed exit evaluation running; auto-rearm only after three consecutive trusted readings below the 16% warning band. Test partial close, stop trigger, API failure, fill delay, and restart behavior.

### P4 — Owner alerts and warning anti-spam

Route every owner-only/informational alert to configured channel routes. Do not use `DISCORD_OWNER_ID` for notification delivery or ask update/migration questions by DM. The portfolio latch itself rearms after the selected three trusted readings below 16%.

## Decisions before live kill-switch implementation

1. **Approved:** Start a new account-equity history from the first trusted Copy `totalEquity` snapshot. The old virtual peak is discarded; tracked drawdown starts at 0%.
2. **Approved:** Automatically re-arm after three consecutive trusted readings below the current 16% warning threshold.
3. **Protection in current code:** Keep the configured bot-managed close evaluator active during entry latches/circuit breakers. No exchange-native Copy TPSL order was verified during the audit; verify before representing it as active protection.
4. **Approved:** Informational/trade/risk updates go to channels only; no DMs for alerts, update discovery, or config migration. Owner ID is not needed for those updates.

No exchange orders were submitted by the assistant. The live Docker rebuild returned healthy and the first risk snapshot records `equity_source=BloFin Copy Trading totalEquity`, peak `$375.460214`, and drawdown `0%`. During the first post-restart strategy cycle, the bot itself filled SPCX order `16988309` (502 contracts @ `$149.1693`). Read-only Copy history also shows DOGE sells `16972650` and `16973342` (3 contracts each) and a filled USELESS sell `16988265` (40 contracts), while the live scheduler DB still showed DOGE `7.5` and USELESS `40`; live Copy positions report DOGE `1.5` remaining and no USELESS position. Do not edit `state.db` while the service is running; an offline fill reconciliation needs a separate approved stop/backup/recovery operation. Startup config-version persistence also logged `rename ... config.json: device or resource busy` because the config is bind-mounted as a single file; the service stayed healthy and completed risk initialization.

## Follow-up (2026-09-28)

- With `go-trader-live` stopped, a consistent pre-reconciliation DB backup was preserved. The approved offline reconciliation added 12 confirmed Copy closes: DOGE `16972650`, `16973342`, `16989798`; USELESS `16988265`; SPX `16988362`; SPCX `16988850`; XLM `16989417`; HYPE `16992857`; ETH `16993599`, `16993685`, `16993686`, and `16994095`.
- Main DB verification: `PRAGMA integrity_check=ok`, exactly 12 close trades for those exchange order IDs, no open positions, and the existing Copy-equity baseline/high-water mark/latch fields preserved. Staging recovery also passed before applying it to the main DB.
- The last read-only Copy API snapshot reported no open positions and `totalEquity=351.3087 USDT`. SPCX production mark-price was confirmed by BloFin (`149.26` at the latest check).
- Mark fetch now uses `/api/v1/market/mark-price`, honors `BLOFIN_BASE_URL`, and is wired into scheduler cycle valuation, `/status`, and summaries. The generic symbol fetch is now spot-only so a third-party spot quote cannot masquerade as a BloFin perps mark. Mocked endpoint/merge tests, the complete Go suite, and `go vet` pass.
- The service was rebuilt and restarted at 2026-09-28 08:27 local time. `/health` and authorized `/status` both returned HTTP 200; the first cycle printed SPCX `$149.02`, and `/status` returned SPCX `$149.03` with the existing Copy-equity source/baseline, 7.83% drawdown, and inactive kill latch. A subsequent Copy API snapshot still showed no open positions. The bind-mounted `config.json` rename/version-persistence issue noted above remains unresolved.
