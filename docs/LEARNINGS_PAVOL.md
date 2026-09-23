# LEARNINGS PAVOL — BloFin live/spot prevádzka

Praktické poznatky z nasadenia (2026-09). Súvisí s `GO-TRADER_OVERRIDES_PAVOL.md`.

## 1. BloFin kontrakty vs mince (kritické)

- BloFin perps `size` = **kontrakty**, nie mince. Každý inštrument má `contractValue`
  (DOGE 1000, XLM 100, BTC 0.001...), `lotSize` a `minSize` z `/api/v1/market/instruments`.
- Všetky výpočty, ktoré miešajú qty × price, MUSIA násobiť `Position.Multiplier`
  (= contractValue): PnL close, margin notional (risk), PortfolioValue.
- `check_price.py` NESMIE zaokrúhľovať (round na 2 desatinné pri 75x páke = chyba $20).
- `collectPriceSymbols` musí obsahovať aj BloFin perps symboly (pôvodne len spot).
- Opravené: `posMult()` helper (portfolio.go), risk.go notional, `quantize_size`
  (kontrakty + lotSize floor + epsilon proti float chybe), Multiplier z fillu.

## 2. Copy trading cez API

- `brokerId` v docs "No", ale prax: bez poľa → 152012, s práznym `""` → prejde.
  Posielať `"brokerId": ""` (alebo `BLOFIN_BROKER_ID` env).
- `copytrading/instruments` bez auth vráti globálny zoznam, s auth per-účet
  (prázdny = účet nemá povolené inštrumenty).
- API kľúč MUSÍ byť typu **API Transaction** (MCP kľúč vracia "not supported").
- IP whitelist na kľúči je povinný, inak všetko padá (aj read-only).
- Spot copy **nemá verejné API** (ani balance, ani orders) — len aplikácia.

## 3. Live close sizing

- `runBloFinExecuteOrder` IGNOROVAL `CloseFraction` → partial close sa na burze
  vykonal ako FULL close, DB si myslela partial. Vždy: close size = posQty × fraction.
- Overovať burzu vs DB po každom close (positions-by-contract).

## 4. Notifikácie

- Execute fail len logoval, na Discord nič nešlo (615 tichých zlyhaní za týždeň).
  Pridané `notifyScriptFailure` do oboch error vetiev (throttle/dedup existuje).
- Script-failure alert má prah **3 po sebe idúce** chyby — prvé 2 sú tiché.
- Trade alerty pre sub-$1 coiny: `fmtComma` (celé $) → vždy $0. Používať `fmtPrice`.

## 5. UI / dashboard

- `/api/strategies/overview` pre 1500+ stratégií: fetch cien 1× + 5-min cache,
  inak timeout. Detail: sparkline len pre aktívnu stratégiu (nie všetkých 1526).
- `UIStrategyOverview` MUSÍ obsahovať `Type`, inak filtre nefungujú.
- DD v UI = equity DD `(peak-value)/peak`. Margin DD z risk engine je iná metrika
  (náladová na páku) — nemiešať.
- Sort číselných stĺpcov: pridať kľúč do `sortValue`, inak radí ako text.

## 6. DB hygiena

- Lifetime štatistiky (wins/losses/Trades) sa počítajú z `trades`
  (`is_close=1`, agregácia po `position_id`), NIE z `closed_positions`.
  Ručné zásahy musia opraviť VŠETKY tabuľky: positions, trades, closed_positions,
  trade_diagnostics, strategies.cash.
- `trade_diagnostics` MFE/MAE/capture sa počítajú z candles pri close evente;
  ručne vložené riadky ostávajú `pending` — dopočítať ručne alebo nechať.
- SQLite WAL + `mode=ro` = starý snapshot. Čítať priamo (query_only) alebo
  zastaviť kontajner. Zápis do pavol-owned DB len cez dočasný kontajner ako root.
- `bls-*` po kapitálovom resete ($1000→$100): vynulovať aj `risk_peak_value`
  (inak-DD 93% z peak 1533), jinak DB cash nesedí s configom.
- Nové spot pozície: `multiplier=1` (paper cesta ho nenastavuje, migrácia nie vždy dobehne).

## 7. CI/CD

- CI `Format check` = `gofmt -l *.go` v scheduler/. Lokálne púšťať presne tak
  (nie na adresár). Import order (`os` vs `net/http`) chytá tiež.
- `TestAgentInfoEnvVarsCoverSource`: každý nový `os.Getenv` MUSÍ do `agentInfoEnvVars`.
- `gh` na hoste je prihlásené (openclaw-bot má vlastný login) — `gh run list/watch`.

## 8. Infra

- Dockge nevidí `/home/pavol/GitRepositories` → pridaný ro mount do jeho compose.
  Buildy z Dockge UI fungujú až po tomto.
- SWAG `swag` kontajner je v `lsio` sieti (nie `nginx_over_tailscale-swag`).
  Oba go-trader kontajnery sú v `lsio`.
- `.env` súbory sú pavol-owned, openclaw-bot do nich nevie písať (ani čítať
  secrets do chatu). `INSTANCE_LABEL` a credentials dopĺňa Pavol ručne.
- Binance.US je z hosta nedostupná → `check_price.py` má BloFin fallback.
- `fetch_blofin_*.py` skripty majú rozbitý `import os` (nepoužívať, volať adapter priamo).
