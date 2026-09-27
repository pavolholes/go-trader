# BloFin Copy Trading Cross-Margin Risk — Audit and Fix Plan

**Status:** Audit findings recorded; implementation of exchange-risk and kill-switch changes is pending operator policy decisions.

**Currently deployed:** portfolio warning threshold is configured at 80% of the 20% model limit (16%), with a simpler message and 2pp escalation. Those warning inputs are still model-derived and must not be represented as BloFin account-equity or cross-margin measurements.

## Executive summary

The current portfolio warning uses internal virtual strategy-book value and estimated per-position margin. Its labels sound like BloFin account equity and cross margin, but they are not those exchange metrics. The global kill-switch is likewise based on the virtual strategy book when equity is available, and the live close planner is not wired to BloFin Copy Trading. A recent exchange-confirmed SPCX partial close was also skipped by the live ledger.

## Audit snapshot (2026-09-27)

- BloFin Copy Trading asset balance: **$396.447 USDT**.
- BloFin Copy Trading perps `totalEquity`: **$396.598**, available **$346.164**.
- At the same audit, Go `/status` reported internal `TotalPV` **$1,223.04**, peak **$1,253.32**, equity drawdown **2.28%**, and modeled perps-margin drawdown **7.09%**. These are not account equity/margin fields.
- BloFin returned 6 open positions by order before the SPCX close sequence and **5 open positions / 0 open orders** afterward.
- At the same audit, Go `/status` reported internal `TotalPV` **$1,223.04**, peak **$1,253.32**, equity drawdown **2.28%**, and modeled perps-margin drawdown **7.09%**. These are not account equity/margin fields.
- SPCX parent position `16931262` had 504 original contracts. Three strategy SELL close orders filled: `16967850` 201 contracts (PnL +$0.1809, fee $0.17948898), `16968008` 201 (PnL +$0.201, fee $0.17950104), and `16968123` 102 (PnL +$0.102, fee $0.09109008). Total 504 closed; gross PnL +$0.4839 and fees $0.45008010. BloFin position history confirms the parent position is closed.
- The strategy requested a 201.6-contract 40% partial close on three consecutive cycles because runtime/DB stayed at 504. BloFin filled the available quantity each time; the last fill closed the remaining 102. The live log reported `BloFin live order without fill ... skipping DB write` after each fill. Go runtime and `state.db` still show quantity 504 and only the opening trade; the exchange has no SPCX position.
- The other five exchange positions remain open. No SPX order was submitted in the inspected 30-minute window; the SPX row is an existing position, not one of these SPCX close orders.
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

Copy order-history IDs `16967850`, `16968008`, and `16968123` record the three fills; the first position-detail close ID is `7363438`, a different ID namespace. Direct polling by Copy history ID returns the fills, but live execution still reports no fill and skips DB writes. Runtime/DB therefore remain at 504 after BloFin fully closed the position. The stale quantity can corrupt risk inputs and cause repeated close attempts; the close guard prevented orders after exchange inventory reached zero.

### E. Discord owner DM is not configured; threshold can chatter

- `SendOwnerDM` is a no-op when owner ID is blank. Configure `DISCORD_OWNER_ID` or `discord.owner_id` and verify the DM route end-to-end.
- The 16% threshold is currently applied to model equity or modeled margin. The 2pp escalation and six-hour reminder do not suppress immediate notifications after the risk state leaves and re-enters the band; add a re-entry cooldown/hysteresis.

## Proposed target design

1. **Account-of-record:** Use BloFin Copy Trading account `totalEquity` for actual account drawdown; keep wallet balance, available balance, and unrealized PnL separately named. Establish and persist a new exchange-equity peak baseline; never compare the existing virtual `$1,253` peak to the account's `$396` balance.
2. **Cross-margin measurement:** Prefer exchange-reported account/position fields. If true used margin is unavailable, label any notional/leverage figure as a model estimate and do not present it as BloFin Cross margin or as a liquidation threshold.
3. **Kill policy — operator decision required:**
   - **Auto-flatten:** on confirmed account-equity breach, close all Copy positions by contract, verify exchange fills and flat state, then keep the latch until explicit reset; or
   - **Entry-halt only:** block new/increasing exposure, keep exits/partial TP/SL management running, and require an operator to close remaining positions.
4. **Close-only handling:** A portfolio/strategy breaker must reject new entries and scale-ins but permit risk-reducing closes, partial take-profits, stop maintenance, and reconciliation.
5. **BloFin close confirmation:** Implement parent-position/order-ID correlation, stable `clientOrderId` where supported, exact fill/fee booking, retries that cannot over-close, and exchange-confirmed flat checks. Never clear virtual position state from a mark price alone.
6. **Protection:** Decide whether Copy positions require exchange-native SL/TP attachments or a continuously running bot-managed exit monitor. Add a visible protection-status check and alert when a live position has neither verified exchange protection nor active management.
7. **Notifications:** Keep the warning concise, name the actual metric/source and state whether action is required. Add hysteresis/cooldown for re-entry, and enable owner DMs only after configuring and testing the owner ID.

## Implementation phases

### P0 — Repair current SPCX state

1. Obtain authorization to stop automation briefly, take a consistent SQLite backup, and stage reconciliation from Copy Trading position/order history.
2. Rebuild SPCX parent `16931262` as fully closed from the three confirmed fills, booking all three fees and realized PnL and removing its stale open-position row.
3. Install only after integrity/count validation, preserve backups, restart, and compare all five remaining order IDs/available quantities with BloFin.

### P1 — Make fill correlation reliable

Add tests for differing placement, Copy-history, and position-detail ID namespaces; instrument the acknowledgement identifiers without logging secrets; ensure a confirmed exchange fill reaches Go before its DB write decision.

### P2 — Base risk on the actual Copy account

Fetch and persist the Copy account equity/peak snapshot, replace virtual aggregate equity as the live account kill-switch basis, label estimated margin explicitly, and define reset/re-baseline rules.

### P3 — Implement the chosen kill/exit policy

Add a BloFin Copy closer and confirmed-flat verification if auto-flatten is selected. In either policy, ensure latches suppress entries but never suppress position-reducing management. Test partial close, stop trigger, API failure, fill delay, restart, and reset behavior.

### P4 — Owner alerts and warning anti-spam

Set/verify `DISCORD_OWNER_ID`; test delivery and failures. Add re-entry hysteresis so fluctuations around the warning threshold do not produce repeated channel posts.

## Decisions before live kill-switch implementation

1. At the real BloFin account-equity threshold, should the bot automatically close all Copy positions, or only block new exposure and ask you to manage open positions?
2. Should BloFin risk be protected by exchange-native SL/TP orders, bot-managed exits that continue during breakers, or both?
3. Provide the Discord numeric user ID or set `DISCORD_OWNER_ID` in the live environment if private DMs are desired.

No exchange positions were closed and no database changes were made during this audit.
