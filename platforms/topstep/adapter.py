
import os
import sys
import time
from datetime import datetime, timezone, timedelta

API_BASE_URL = "https://api.topstepx.com"

YAHOO_SYMBOL_MAP = {
    "ES": "ES=F",
    "NQ": "NQ=F",
    "MES": "MES=F",
    "MNQ": "MNQ=F",
    "CL": "CL=F",
    "GC": "GC=F",
}

CONTRACT_SPECS = {
    "ES": {"tick_size": 0.25, "tick_value": 12.50, "multiplier": 50, "margin": 15400, "type": "index"},
    "NQ": {"tick_size": 0.25, "tick_value": 5.00, "multiplier": 20, "margin": 21000, "type": "index"},
    "MES": {"tick_size": 0.25, "tick_value": 1.25, "multiplier": 5, "margin": 1540, "type": "index"},
    "MNQ": {"tick_size": 0.25, "tick_value": 0.50, "multiplier": 2, "margin": 2100, "type": "index"},
    "CL": {"tick_size": 0.01, "tick_value": 10.00, "multiplier": 1000, "margin": 7500, "type": "energy"},
    "GC": {"tick_size": 0.10, "tick_value": 10.00, "multiplier": 100, "margin": 11000, "type": "metals"},
}


def _normalize_topstep_fill(f):
    try:
        ts_ms = int(f.get("timestamp", f.get("ts_ms", 0)))
    except (TypeError, ValueError):
        return None
    return {
        "fill_id": str(f.get("id", f.get("fill_id", ""))),
        "ts_ms": ts_ms,
        "symbol": (f.get("symbol") or "").upper(),
        "kind": (f.get("kind") or "").lower(),
        "realized_pnl": float(f.get("realizedPnl", f.get("realized_pnl", 0)) or 0),
        "fee": float(f.get("fee", f.get("commission", 0)) or 0),
    }


class TopStepExchangeAdapter:

    def __init__(self, mode="paper"):
        self._mode = mode
        self._api_key = os.environ.get("TOPSTEP_API_KEY", "")
        self._api_secret = os.environ.get("TOPSTEP_API_SECRET", "")
        self._account_id = os.environ.get("TOPSTEP_ACCOUNT_ID", "")
        self._session = None

        if mode == "live":
            if not self._api_key or not self._api_secret or not self._account_id:
                raise RuntimeError(
                    "Live mode requires TOPSTEP_API_KEY, TOPSTEP_API_SECRET, "
                    "and TOPSTEP_ACCOUNT_ID environment variables"
                )
            try:
                import requests
                self._session = requests.Session()
                self._session.headers.update({
                    "X-API-Key": self._api_key,
                    "X-API-Secret": self._api_secret,
                    "Content-Type": "application/json",
                })
            except ImportError:
                raise ImportError("requests package required for live mode. Run: uv sync")

    @property
    def is_live(self) -> bool:
        return self._mode == "live" and self._session is not None

    @property
    def mode(self) -> str:
        return self._mode

    @property
    def name(self) -> str:
        return "topstep"


    def get_contract_spec(self, symbol: str) -> dict:
        spec = CONTRACT_SPECS.get(symbol)
        if spec is None:
            raise ValueError(f"Unknown symbol: {symbol}. Supported: {list(CONTRACT_SPECS.keys())}")
        return dict(spec)


    def get_price(self, symbol: str) -> float:
        if not self.is_live:
            return self._get_yahoo_price(symbol)
        try:
            resp = self._session.get(
                f"{API_BASE_URL}/v1/market/quote",
                params={"symbol": symbol, "accountId": self._account_id},
                timeout=10,
            )
            resp.raise_for_status()
            data = resp.json()
            return float(data.get("lastPrice", 0))
        except Exception as e:
            print(f"[topstep] get_price error: {e}", file=sys.stderr)
            return 0.0

    def get_ohlcv(self, symbol: str, interval: str = "1h", limit: int = 200) -> list:
        if not self.is_live:
            return self._get_yahoo_ohlcv(symbol, interval, limit)
        try:
            resp = self._session.get(
                f"{API_BASE_URL}/v1/market/candles",
                params={
                    "symbol": symbol,
                    "interval": interval,
                    "limit": limit,
                    "accountId": self._account_id,
                },
                timeout=15,
            )
            resp.raise_for_status()
            candles = resp.json().get("candles", [])
            result = []
            for c in candles:
                result.append([
                    int(c.get("timestamp", 0)),
                    float(c.get("open", 0)),
                    float(c.get("high", 0)),
                    float(c.get("low", 0)),
                    float(c.get("close", 0)),
                    float(c.get("volume", 0)),
                ])
            return result
        except Exception as e:
            print(f"[topstep] get_ohlcv error: {e}", file=sys.stderr)
            return []


    def get_open_positions(self) -> list:
        if not self.is_live:
            return []
        try:
            return self._fetch_open_positions()
        except Exception as e:
            print(f"[topstep] get_open_positions error: {e}", file=sys.stderr)
            return []

    def get_open_positions_raise(self) -> list:
        if self._mode != "live":
            raise RuntimeError("TopStep adapter not in live mode")
        if self._session is None:
            raise RuntimeError(
                "TopStep live session not initialized — missing "
                "TOPSTEP_API_KEY / TOPSTEP_API_SECRET / TOPSTEP_ACCOUNT_ID"
            )
        return self._fetch_open_positions()

    def _fetch_open_positions(self) -> list:
        resp = self._session.get(
            f"{API_BASE_URL}/v1/account/positions",
            params={"accountId": self._account_id},
            timeout=10,
        )
        resp.raise_for_status()
        positions = []
        for pos in resp.json().get("positions", []):
            qty = int(pos.get("quantity", 0))
            if qty == 0:
                continue
            positions.append({
                "symbol": pos.get("symbol", ""),
                "quantity": qty,
                "avg_price": float(pos.get("avgPrice", 0)),
                "side": "long" if qty > 0 else "short",
                "unrealized_pnl": float(pos.get("unrealizedPnl", 0)),
            })
        return positions

    def get_account_equity_and_upnl(self):
        if not self.is_live:
            raise RuntimeError("get_account_equity_and_upnl requires live mode")
        resp = self._session.get(
            f"{API_BASE_URL}/v1/account/balance",
            params={"accountId": self._account_id},
            timeout=10,
        )
        resp.raise_for_status()
        data = resp.json()
        if "equity" not in data:
            raise ValueError("TopStep /v1/account/balance response missing 'equity' field")
        equity = float(data.get("equity", 0))
        cash = float(data.get("cashBalance", data.get("balance", equity)))
        return equity, equity - cash

    def get_account_fills(self, since_ms=0, page_limit=100, max_fills=10000):
        if not self.is_live:
            raise RuntimeError("get_account_fills requires live mode")
        collected = {}
        cursor = int(since_ms or 0)
        capped = False
        for _ in range(max(1, max_fills // max(1, page_limit)) + 2):
            resp = self._session.get(
                f"{API_BASE_URL}/v1/account/fills",
                params={
                    "accountId": self._account_id,
                    "sinceMs": cursor,
                    "limit": int(page_limit),
                },
                timeout=15,
            )
            resp.raise_for_status()
            page = resp.json().get("fills", []) or []
            if not page:
                break
            before = len(collected)
            page_last_ts = cursor
            for f in page:
                fill = _normalize_topstep_fill(f)
                if fill is None:
                    continue
                if fill["ts_ms"] > page_last_ts:
                    page_last_ts = fill["ts_ms"]
                key = fill["fill_id"] or f"{fill['kind']}:{fill['ts_ms']}:{fill['symbol']}"
                collected[key] = fill
            added = len(collected) - before
            if len(collected) >= max_fills:
                capped = True
                break
            if len(page) < int(page_limit):
                break
            if page_last_ts <= cursor and added == 0:
                capped = True
                break
            cursor = page_last_ts
        else:
            capped = True
        fills = sorted(collected.values(), key=lambda x: x["ts_ms"])
        if len(fills) > max_fills:
            fills = fills[:max_fills]
        return fills, capped


    def _get_yahoo_price(self, symbol: str) -> float:
        yahoo_sym = YAHOO_SYMBOL_MAP.get(symbol)
        if not yahoo_sym:
            return 0.0
        try:
            candles = self._get_yahoo_chart_ohlcv(symbol, "1d", 1)
            if candles:
                return float(candles[-1][4])
        except Exception as e:
            print(f"[topstep] Yahoo Chart price fetch failed for {symbol}: {e}", file=sys.stderr)
        try:
            import yfinance as yf
            ticker = yf.Ticker(yahoo_sym)
            hist = ticker.history(period="1d")
            if hist.empty:
                return 0.0
            return float(hist["Close"].iloc[-1])
        except ImportError:
            print("[topstep] yfinance not installed — paper mode has no price data. Run: uv add yfinance", file=sys.stderr)
            return 0.0
        except Exception as e:
            print(f"[topstep] yahoo price error for {symbol}: {e}", file=sys.stderr)
            return 0.0

    def _get_yahoo_chart_ohlcv(self, symbol: str, interval: str, limit: int) -> list:
        """Fetch public Yahoo chart JSON directly, without fc.yahoo cookie auth.

        Yahoo's ``fc.yahoo.com`` cookie endpoint is intermittently unreachable
        from the demo network. The public chart endpoint works with browser
        headers and gives the same timestamp/OHLCV series; yfinance remains a
        caller-side fallback if this endpoint is unavailable.
        """
        yahoo_sym = YAHOO_SYMBOL_MAP.get(symbol)
        if not yahoo_sym or int(limit) <= 0:
            return []
        try:
            import re
            import requests
        except ImportError as e:
            raise RuntimeError(f"requests unavailable for Yahoo Chart API: {e}") from e

        normalized = str(interval).strip().lower()
        match = re.fullmatch(r"(\d+)(m|h|d)", normalized)
        if not match:
            raise ValueError(f"unsupported Yahoo interval {interval!r}")
        count = int(match.group(1))
        unit_seconds = {"m": 60, "h": 3600, "d": 86400}[match.group(2)]
        seconds_per_bar = count * unit_seconds

        # Ask for a safety margin over the requested trailing bar count to
        # cover weekends/maintenance gaps. Yahoo intraday chart data is capped
        # at 60d (1m at 7d); daily data has no such short cap.
        lookback_seconds = int(max(int(limit), 1) * seconds_per_bar * 2.2)
        max_days = 7 if normalized == "1m" else (
            60 if match.group(2) == "m" else None
        )
        if max_days is not None:
            lookback_seconds = min(lookback_seconds, max_days * 86400)
        now_s = int(time.time())
        params = {
            "period1": str(now_s - lookback_seconds),
            "period2": str(now_s),
            "interval": normalized,
            "includePrePost": "true",
            "events": "div,splits",
        }
        headers = {
            "User-Agent": (
                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
                "AppleWebKit/537.36 Chrome/131 Safari/537.36"
            ),
            "Accept": "application/json,text/plain,*/*",
            "Origin": "https://finance.yahoo.com",
            "Referer": "https://finance.yahoo.com/",
        }
        response = requests.get(
            f"https://query1.finance.yahoo.com/v8/finance/chart/{yahoo_sym}",
            params=params,
            headers=headers,
            timeout=15,
        )
        response.raise_for_status()
        payload = response.json()
        chart = payload.get("chart") or {}
        if chart.get("error"):
            raise RuntimeError(f"Yahoo Chart API error: {chart['error']}")
        results = chart.get("result") or []
        if not results:
            return []
        result = results[0]
        timestamps = result.get("timestamp") or []
        indicators = result.get("indicators") or {}
        quotes = indicators.get("quote") or []
        if not quotes:
            return []
        quote = quotes[0]
        opens = quote.get("open") or []
        highs = quote.get("high") or []
        lows = quote.get("low") or []
        closes = quote.get("close") or []
        volumes = quote.get("volume") or []
        rows = []
        for idx, ts in enumerate(timestamps):
            try:
                o, h, low, close = opens[idx], highs[idx], lows[idx], closes[idx]
                if o is None or h is None or low is None or close is None:
                    continue
                volume = volumes[idx] if idx < len(volumes) and volumes[idx] is not None else 0
                rows.append([
                    int(ts * 1000), float(o), float(h), float(low),
                    float(close), float(volume),
                ])
            except (IndexError, TypeError, ValueError, OverflowError):
                continue
        rows.sort(key=lambda row: row[0])
        return rows[-int(limit):]

    def _get_yahoo_ohlcv(self, symbol: str, interval: str = "1h", limit: int = 200) -> list:
        yahoo_sym = YAHOO_SYMBOL_MAP.get(symbol)
        if not yahoo_sym:
            return []
        try:
            chart_rows = self._get_yahoo_chart_ohlcv(symbol, interval, limit)
            if chart_rows:
                return chart_rows
        except Exception as e:
            print(f"[topstep] Yahoo Chart OHLCV fetch failed for {symbol}: {e}; falling back to yfinance", file=sys.stderr)
        try:
            import yfinance as yf
        except ImportError:
            print("[topstep] yfinance not installed — paper mode has no OHLCV data. Run: uv add yfinance", file=sys.stderr)
            return []
        # Yahoo's cookie/auth endpoint is flaky from datacenter networks:
        # a single failed burst used to fail-closed the whole check.
        # Bounded retry (5 attempts, 9s total backoff) rides out transient
        # Yahoo egress failures while staying inside the Go script timeout; a
        # persistent outage still returns [] and fail-closes downstream.
        last_error = None
        retry_delays = (1, 2, 3, 3)
        for attempt in range(1, len(retry_delays) + 2):
            try:
                yf_interval = str(interval).strip().lower()
                if yf_interval == "1m":
                    # Yahoo retains 1-minute candles for only the last week.
                    period = "7d"
                elif yf_interval in ("2m", "5m", "15m", "30m", "90m"):
                    # 5d yielded only ~346 15m NQ bars, too few for ob_touch's
                    # 50-bar swing pivots. Yahoo supports these intraday bars
                    # for up to 60 days; return the requested trailing window.
                    period = "60d"
                elif yf_interval in ("1h", "60m"):
                    period = "30d"
                else:
                    period = "1y"
                ticker = yf.Ticker(yahoo_sym)
                hist = ticker.history(period=period, interval=yf_interval)
                if hist.empty:
                    last_error = "empty history"
                    raise ValueError("empty history")
                result = []
                for idx, row in hist.iterrows():
                    ts_ms = int(idx.timestamp() * 1000)
                    result.append([
                        ts_ms,
                        float(row["Open"]),
                        float(row["High"]),
                        float(row["Low"]),
                        float(row["Close"]),
                        float(row.get("Volume", 0)),
                    ])
                return result[-limit:]
            except Exception as e:
                last_error = e
                print(f"[topstep] yahoo ohlcv error for {symbol} (attempt {attempt}/5): {e}", file=sys.stderr)
                if attempt <= len(retry_delays):
                    time.sleep(retry_delays[attempt - 1])
        print(f"[topstep] yahoo ohlcv error for {symbol}: {last_error}", file=sys.stderr)
        return []


    def market_open(self, symbol: str, is_buy: bool, contracts: int) -> dict:
        if not self.is_live:
            raise RuntimeError("market_open requires live mode")
        contracts = int(contracts)
        if contracts <= 0:
            raise ValueError("contracts must be > 0")
        resp = self._session.post(
            f"{API_BASE_URL}/v1/order/market",
            json={
                "accountId": self._account_id,
                "symbol": symbol,
                "side": "buy" if is_buy else "sell",
                "quantity": contracts,
            },
            timeout=10,
        )
        resp.raise_for_status()
        return resp.json()

    def market_close(self, symbol: str) -> dict:
        if not self.is_live:
            raise RuntimeError("market_close requires live mode")
        resp = self._session.post(
            f"{API_BASE_URL}/v1/order/close",
            json={
                "accountId": self._account_id,
                "symbol": symbol,
            },
            timeout=10,
        )
        resp.raise_for_status()
        return resp.json()


    def is_market_open(self) -> bool:
        try:
            from zoneinfo import ZoneInfo
        except ImportError:
            from backports.zoneinfo import ZoneInfo

        now = datetime.now(ZoneInfo("America/New_York"))
        weekday = now.weekday()
        hour = now.hour
        minute = now.minute
        current_minutes = hour * 60 + minute

        maintenance_start = 17 * 60
        maintenance_end = 18 * 60
        if maintenance_start <= current_minutes < maintenance_end:
            return False

        if weekday == 5:
            return False

        if weekday == 6:
            return current_minutes >= maintenance_end

        if weekday == 4:
            return current_minutes < maintenance_start

        return True
