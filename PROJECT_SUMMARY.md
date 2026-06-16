# go-trader project — vecné zhrnutie

## Čo to je
Multi-strategy paper trading engine. Beží kontajner s 291 stratégiami na BloFin perps v `--mode=paper` (virtuálny trading, žiadne reálne objekty). Zbiera dáta pre dlhodobý bear/bull research.

---

## Architektúra

```
scheduler/main.go          — Go entrypoint, event loop
├── config.go              — načítava config.json
├── executor.go            — spúšťa Python strategy skripty
├── regime*.go             — regime detection (ADX, ATR)
├── blofin_close.go        — BloFin close position logika
├── blofin_marks.go        — BloFin mark price fetching
├── state.go               — DB state (SQLite cez modernc.org/sqlite)
├── ui_server.go           — HTTP server na porte 8100
├── ui_tuner.go            — live tuner config panel
├── static/ui/             — frontend (HTML + CSS + JS)
│   ├── index.html
│   ├── styles.css
│   └── app.js
└── platforms/blofin/      — Python BloFin adapter
    └── adapter.py         — BloFinExchangeAdapter trieda (REST API, HMAC-SHA256)
```

---

## Deployment

- **Host**: `host-gate` (SSH)
- **Container**: `go-trader-demo`, Docker Compose v `/home/pavol/DockerVolumes/dockge/go-trader-demo/`
- **Port**: 8100
- **Build**: inline Dockerfile v compose.yaml (multistage: Go build → Python slim)
- **Volume bindy**:
  - `config.json` → `/app/scheduler/config.json`
  - `/home/pavol/DockerVolumes/go-trader-demo/data/` → `/app/data/`
- **State DB**: `/home/pavol/DockerVolumes/go-trader-demo/data/state.db`
- **Logy**: `/app/data/logs/` (cez `log_dir` v config)

### Build & deploy cyklus

```bash
ssh host-gate
cd /home/pavol/DockerVolumes/dockge/go-trader-demo
docker compose build
docker compose up -d
```

---

## Go modul

- **Module name**: `trading-scheduler`
- **Go verzia**: 1.26.2
- **Dependencies**: `modernc.org/sqlite`, `github.com/bwmarrin/discordgo`, `github.com/google/uuid`
- **181 `.go` súborov** v `scheduler/`

---

## Python súbory

| Súbor | Účel |
|-------|-------|
| `shared_scripts/check_blofin.py` | Hlavný strategy script pre BloFin |
| `shared_scripts/check_balance.py` | Kontrola zostatku |
| `shared_scripts/check_regime.py` | Regime detection (ADX) |
| `shared_scripts/close_blofin_position.py` | Close position na BloFin |
| `shared_scripts/fetch_candles.py` | Fetch OHLCV dát |
| `shared_scripts/fetch_blofin_balance.py` | Fetch BloFin balance |
| `shared_scripts/fetch_blofin_positions.py` | Fetch BloFin pozície |
| `shared_scripts/simulate_strategy.py` | Simulácia stratégie |
| `platforms/blofin/adapter.py` | BloFinExchangeAdapter (REST API, HMAC-SHA256) |
| `platforms/okx/adapter.py` | OKX adapter |
| `platforms/hyperliquid/adapter.py` | HyperLiquid adapter |

- **Python deps**: `pandas`, `requests`, `numpy`, `ccxt`
- **Adapter ENV vars**: `BLOFIN_API_KEY`, `BLOFIN_API_SECRET`, `BLOFIN_PASSPHRASE`, `BLOFIN_BASE_URL`

---

## Config štruktúra (`config.json`)

**Umiestnenie**: `/home/pavol/DockerVolumes/go-trader-demo/config/config.json`

### Top-level kľúče

| Kľúč | Hodnota |
|-------|---------|
| `config_version` | 16 |
| `interval_seconds` | 300 (hlavný cyklus) |
| `db_file` | `data/state.db` |
| `status_port` | 8100 |
| `portfolio_risk.max_drawdown_pct` | 100 (portfóliový CB deaktivovaný) |
| `regime.enabled` | true |
| `regime.period` | 14 |
| `regime.adx_threshold` | 20 |
| `regime.windows` | short: 7, medium: 14, long: 30 |
| `discord.enabled` | true (notifikácie deaktivované kvôli noisu) |
| `log_dir` | `/app/data/logs` |

### Template stratégie (291 ks)

```json
{
  "id": "bl-momentum-btc-1h",
  "type": "perps",
  "platform": "blofin",
  "script": "shared_scripts/check_blofin.py",
  "args": ["momentum", "BTC", "1h", "--mode=paper"],
  "capital": 1000,
  "leverage": 150,
  "sizing_leverage": 0,
  "margin_per_trade_usd": 10,
  "direction": "long",
  "close_strategy": {
    "name": "tiered_tp_atr_regime",
    "params": { "use_defaults": true, "sl_atr_mult": 1.5 }
  },
  "max_drawdown_pct": 50,
  "interval_seconds": 300
}
```

### Kľúčové nastavenia

- **capital: 1000** (fixed, nie `capital_pct`)
- **margin_per_trade_usd: 10** ($10 margin na obchod)
- **Leverage**: BTC=150x, ETH=150x, SOL=125x, BNB=75x, HYPE=75x
- **Direction**: `long` (väčšina), `both` (niektoré)
- **SL**: `close_strategy.params.sl_atr_mult: 1.5`
- **Per-strategy CB**: `max_drawdown_pct: 50`
- **Portfolio CB**: 100% (deaktivovaný)
- **Discord**: deaktivovaný
- **Režim**: `--mode=paper` (všetky stratégie)
- **Close strategy**: `tiered_tp_atr_regime` (regime-aware TP tiers + ATR-based SL)

---

## BloFin API

- Demo base URL: `https://demo-trading-openapi.blofin.com`
- Autentifikácia: HMAC-SHA256 (API key, secret, passphrase)
- `get_ohlcv()` auto-pripája `-USDT` suffix (ak symbol nemá pomlčku)
- Podporuje: market, limit, post_only, fok, ioc; trigger orders; TP/SL; leverage setting

---

## Významné Go súbory

| Súbor | Účel |
|-------|-------|
| `main.go` | Event loop entrypoint |
| `config.go` | Config parsing, leverage validation (max 150) |
| `executor.go` | Python script executor (signal check → close check cyklus) |
| `state.go` | SQLite state perzistencia |
| `regime.go` | ADX/ATR regime detection |
| `regime_store.go` | Cached regime store (vracia `"blofin"` pre BloFin perps) |
| `regime_unified.go` | Unified regime config + window spec |
| `regime_divergence.go` | Regime window divergence detection |
| `regime_directional_policy.go` | Directional policy based on regime |
| `regime_dynamic_tp_sl.go` | Multi-window ATR pre TP/SL scaling |
| `ui_server.go` | HTTP API, dashboard routes |
| `ui_tuner.go` | UI tuner form builder + patch apply |
| `ui_candles.go` | Candlestick data pre chart |
| `ui_regime.go` | Regime data pre UI |
| `blofin_close.go` | BloFin perps close position |
| `blofin_marks.go` | BloFin mark/index price |
| `config_reload.go` | Hot-reload config |
| `config_migration.go` | Verzie config migrácií |
| `container.go` | Strategy container (agreguje strategie) |
| `strategy_composition.go` | Kompozícia stratégie (entry + close) |
| `circuit_breaker_alert.go` | Circuit breaker logika |
| `db.go` | SQLite DB operácie |
| `discord.go` | Discord notifikácie |

---

## Frontend

- **Port**: 8100
- **UI súbory**: `scheduler/static/ui/` (index.html, styles.css, app.js, lightweight-charts.standalone.production.js)
- **Knižnica**: Lightweight Charts (TradingView)
- **3 views**:
  - **Overview** — tabuľka všetkých stratégií s filtrom, sortom, PnL, WR, Sharpe, regime
  - **Detail** — candlestick chart + trade history + status grid + positions
  - **Tuner** — live parameter edit (apply without restart)
- **Refresh interval**: 5s / 15s / 30s / Off

### API endpointy

| Endpoint | Účel |
|----------|-------|
| `GET /api/strategies/overview` | Všetky stratégie s metrikami |
| `GET /api/status?strategy=STRAT_ID` | Detail stratégie |
| `GET /api/config?strategy=STRAT_ID` | Live config JSON |
| `PATCH /api/config` | Apply tuner changes |
| `GET /api/regime_config` | Regime config |

---

## Užitočné príkazy

```bash
# SSH na host
ssh host-gate

# Rebuild a deploy kontajnera
cd /home/pavol/DockerVolumes/dockge/go-trader-demo
docker compose build && docker compose up -d

# Logy
docker exec go-trader-demo tail -50 /app/scheduler/logs/go-trader.log

# Test strategy cyklu (jeden signal check)
cd /home/pavol/GitRepositories/go-trader
python3 shared_scripts/check_blofin.py momentum BTC 1h --mode=paper

# Overview data z API
curl -s http://localhost:8100/api/strategies/overview | python3 -m json.tool

# Status jednej stratégie
curl -s "http://localhost:8100/api/status?strategy=bl-momentum-btc-1h" | python3 -m json.tool
```

---

## Dôležité cesty

| Cesta | Význam |
|-------|--------|
| `/home/pavol/GitRepositories/go-trader/scheduler/` | Go zdrojáky |
| `/home/pavol/GitRepositories/go-trader/shared_scripts/` | Python strategy skripty |
| `/home/pavol/GitRepositories/go-trader/platforms/` | Exchange adaptéry (blofin, okx, hyperliquid...) |
| `/home/pavol/DockerVolumes/go-trader-demo/config/config.json` | Konfigurácia (291 stratégií) |
| `/home/pavol/DockerVolumes/go-trader-demo/data/state.db` | SQLite state DB |
| `/home/pavol/DockerVolumes/dockge/go-trader-demo/compose.yaml` | Docker Compose |
| `/home/pavol/DockerVolumes/dockge/go-trader-demo/.env` | API kľúče |
