"""BloFin Spot Exchange Adapter — market data for spot (phase 1).

Uses the dedicated spot endpoints (/api/v1/spot/market/*), NOT the
generic /api/v1/market/* endpoints shared with perpetual swaps.

Spot candles return 9 fields per row:
    [ts, open, high, low, close, vol, volCurrency, volCurrencyQuote, confirm]
This adapter normalises to the 6-field format used everywhere else:
    [ts, open, high, low, close, vol]

Phase 1 = market data only (paper signals). Trading methods (balance,
place/cancel order) come in phase 2 after demo proves stable.

Environment variables (same as the perps adapter):
    BLOFIN_API_KEY / BLOFIN_API_SECRET / BLOFIN_PASSPHRASE / BLOFIN_BASE_URL
"""

import os
import sys
from urllib.parse import urlencode

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import requests as http_requests


class BloFinSpotExchangeAdapter:
    """Exchange adapter for BloFin — spot market data (phase 1)."""

    def __init__(self, base_url: str = ""):
        self.base_url = (
            base_url
            or os.environ.get("BLOFIN_BASE_URL", "https://demo-trading-openapi.blofin.com")
        )

    @property
    def name(self) -> str:
        return "blofin_spot"

    @property
    def mode(self) -> str:
        return "paper"

    # ─────────────────────────────────────────────
    # HTTP
    # ─────────────────────────────────────────────

    def _public_get(self, path: str, params: dict = None) -> dict:
        url = f"{self.base_url}{path}"
        if params:
            url += "?" + urlencode(params)
        resp = http_requests.get(url, timeout=15)
        data = resp.json()
        if data.get("code") != "0":
            raise RuntimeError(f"BloFin spot API error {path}: {data.get('msg', data)}")
        return data

    # ─────────────────────────────────────────────
    # Helpers
    # ─────────────────────────────────────────────

    @staticmethod
    def _base(symbol: str) -> str:
        """BTC/USDT, BTC-USDT or BTC -> BTC."""
        return (symbol or "").split("/")[0].split("-")[0].strip().upper()

    @staticmethod
    def _ccxt_to_blofin_bar(interval: str) -> str:
        mapping = {
            "1m": "1m", "3m": "3m", "5m": "5m", "15m": "15m", "30m": "30m",
            "1h": "1H", "2h": "2H", "4h": "4H", "6h": "6H", "8h": "8H",
            "12h": "12H", "1d": "1D", "1D": "1D",
            "1w": "1W", "1W": "1W", "1M": "1M",
        }
        return mapping.get(interval, "1H")

    # ─────────────────────────────────────────────
    # Market data
    # ─────────────────────────────────────────────

    def get_spot_instruments(self) -> list:
        """List live SPOT instruments (USDT-quoted only, state=live)."""
        data = self._public_get("/api/v1/spot/market/instruments", {"instType": "SPOT"})
        out = []
        for it in data.get("data", []):
            if not isinstance(it, dict):
                continue
            if it.get("quoteCurrency") != "USDT":
                continue
            if it.get("state") != "live":
                continue
            out.append(it)
        return out

    def get_spot_tickers(self) -> list:
        """All spot tickers (instType=SPOT). Each has last + volCurrency24h."""
        data = self._public_get("/api/v1/spot/market/tickers", {"instType": "SPOT"})
        tickers = data.get("data", [])
        # Keep only -USDT instruments.
        return [t for t in tickers
                if isinstance(t, dict) and str(t.get("instId", "")).endswith("-USDT")]

    def get_spot_price(self, symbol: str) -> float:
        """Last price for BASE-USDT from the spot tickers endpoint."""
        base = self._base(symbol)
        try:
            data = self._public_get("/api/v1/spot/market/tickers", {
                "instType": "SPOT",
                "instId": f"{base}-USDT",
            })
            tickers = data.get("data", [])
            if tickers:
                price = float(tickers[0].get("last", 0) or 0)
                return price if price > 0 else 0.0
        except Exception:
            pass
        return 0.0

    def get_spot_ohlcv(self, symbol: str, interval: str = "1h", limit: int = 200) -> list:
        """Spot candles via /api/v1/spot/market/candles.

        Returns 6-field rows [ts, open, high, low, close, volume],
        sorted ascending by timestamp.
        """
        base = self._base(symbol)
        bar = self._ccxt_to_blofin_bar(interval)
        data = self._public_get("/api/v1/spot/market/candles", {
            "instType": "SPOT",
            "instId": f"{base}-USDT",
            "bar": bar,
            "limit": str(limit),
        })
        candles = data.get("data", [])
        result = []
        for c in candles:
            try:
                result.append([
                    int(c[0]),
                    float(c[1]),
                    float(c[2]),
                    float(c[3]),
                    float(c[4]),
                    float(c[5]),
                ])
            except (IndexError, ValueError, TypeError):
                continue
        result.sort(key=lambda x: x[0])
        return result
