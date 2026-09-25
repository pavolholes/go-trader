"""BloFin Exchange Adapter — unified interface for perpetual swaps and spot.
Uses direct REST API calls with HMAC-SHA256 authentication.

Supports paper (public API only, no credentials) and
live (real orders on BloFin, API credentials required) modes.

Environment variables:
    BLOFIN_API_KEY       — API key for live trading
    BLOFIN_API_SECRET    — API secret for live trading
    BLOFIN_PASSPHRASE    — API passphrase for live trading
    BLOFIN_BASE_URL      — API base URL (demo or live)
"""

import base64
import hashlib
import hmac
import json
import os
import sys
import time
import math
from typing import Optional
from urllib.parse import urlencode

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "shared_tools"))

import requests as http_requests


class BloFinExchangeAdapter:
    """
    Exchange adapter for BloFin — perpetual swaps (futures).

    Paper mode:  no credentials needed; uses public API for market data.
    Live mode:   requires BLOFIN_API_KEY, BLOFIN_API_SECRET, BLOFIN_PASSPHRASE.
    """

    def __init__(self):
        self.api_key = os.environ.get("BLOFIN_API_KEY", "")
        self.api_secret = os.environ.get("BLOFIN_API_SECRET", "")
        self.passphrase = os.environ.get("BLOFIN_PASSPHRASE", "")
        self.base_url = os.environ.get("BLOFIN_BASE_URL", "https://demo-trading-openapi.blofin.com")

        self._is_live = bool(self.api_key and self.api_secret and self.passphrase)
        self.trade_account = os.environ.get("BLOFIN_TRADE_ACCOUNT", "standard").strip().lower()

    @property
    def is_live(self) -> bool:
        return self._is_live

    @property
    def mode(self) -> str:
        return "live" if self._is_live else "paper"

    @property
    def name(self) -> str:
        return "blofin"

    # ─────────────────────────────────────────────
    # Authentication
    # ─────────────────────────────────────────────

    def _generate_signature(self, method: str, request_path: str, body: str = "") -> tuple[str, str, str]:
        timestamp = str(int(time.time() * 1000))
        nonce = str(int(time.time() * 1000)) + str(int(time.monotonic_ns() % 100000))
        msg = request_path + method.upper() + timestamp + nonce + body
        signature = hmac.new(
            self.api_secret.encode("utf-8"),
            msg.encode("utf-8"),
            hashlib.sha256,
        ).hexdigest()
        signature = base64.b64encode(signature.encode()).decode()
        return timestamp, nonce, signature

    def _headers(self, method: str, request_path: str, body: str = "") -> dict:
        headers = {"Content-Type": "application/json"}
        if self._is_live:
            timestamp, nonce, signature = self._generate_signature(method, request_path, body)
            headers.update({
                "ACCESS-KEY": self.api_key,
                "ACCESS-SIGN": signature,
                "ACCESS-TIMESTAMP": timestamp,
                "ACCESS-NONCE": nonce,
                "ACCESS-PASSPHRASE": self.passphrase,
            })
        return headers

    def _public_get(self, path: str, params: dict = None) -> dict:
        url = f"{self.base_url}{path}"
        if params:
            url += "?" + urlencode(params)
        resp = http_requests.get(url, timeout=15)
        data = resp.json()
        if data.get("code") != "0":
            raise RuntimeError(f"BloFin API error {path}: {data.get('msg', data)}")
        return data

    def _private_get(self, path: str, params: dict = None) -> dict:
        full_path = path
        if params:
            full_path += "?" + urlencode(params)
        headers = self._headers("GET", full_path)
        url = f"{self.base_url}{full_path}"
        resp = http_requests.get(url, headers=headers, timeout=15)
        data = resp.json()
        if data.get("code") != "0":
            raise RuntimeError(f"BloFin API error {path}: {data.get('msg', data)}")
        return data

    def _private_post(self, path: str, body: dict = None) -> dict:
        body_str = json.dumps(body) if body else ""
        headers = self._headers("POST", path, body_str)
        url = f"{self.base_url}{path}"
        resp = http_requests.post(url, headers=headers, data=body_str, timeout=15)
        data = resp.json()
        if data.get("code") != "0":
            raise RuntimeError(f"BloFin API error {path}: {data.get('msg', data)}")
        return data

    # ─────────────────────────────────────────────
    # Market data
    # ─────────────────────────────────────────────

    def get_instruments(self, inst_type: str = "SWAP") -> list:
        data = self._public_get("/api/v1/market/instruments", {"instType": inst_type})
        return data.get("data", [])

    def get_ticker(self, symbol: str) -> dict:
        data = self._public_get("/api/v1/market/tickers", {"instId": symbol})
        tickers = data.get("data", [])
        return tickers[0] if tickers else {}

    def get_mark_price(self, symbol: str) -> dict:
        data = self._public_get("/api/v1/market/mark-price", {"instId": symbol})
        prices = data.get("data", [])
        return prices[0] if prices else {}

    def get_spot_price(self, symbol: str) -> float:
        for inst_id in (f"{symbol}-USDT", f"{symbol}-USDC"):
            try:
                ticker = self.get_ticker(inst_id)
                price = float(ticker.get("last", 0) or ticker.get("askPx", 0))
                if price > 0:
                    return price
            except Exception:
                continue
        return 0.0

    def get_perp_price(self, symbol: str) -> float:
        try:
            ticker = self.get_ticker(f"{symbol}-USDT")
            price = float(ticker.get("last", 0) or ticker.get("askPx", 0))
            return price if price > 0 else 0.0
        except Exception:
            return 0.0

    def get_ohlcv(self, symbol: str, interval: str = "1h", limit: int = 200) -> list:
        bar = self._ccxt_to_blofin_bar(interval)
        # Perps need "-USDT" suffix; keep raw symbol if it already has a dash
        inst_id = symbol if "-" in symbol else f"{symbol}-USDT"
        data = self._public_get("/api/v1/market/candles", {
            "instId": inst_id,
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

    def get_perp_ohlcv(self, symbol: str, interval: str = "1h", limit: int = 200) -> list:
        return self.get_ohlcv(f"{symbol}-USDT", interval, limit)

    def get_funding_rate(self, symbol: str) -> float:
        try:
            data = self._public_get("/api/v1/market/funding-rate", {"instId": f"{symbol}-USDT"})
            rates = data.get("data", [])
            if rates:
                return float(rates[0].get("fundingRate", 0))
        except Exception:
            pass
        return 0.0

    def get_funding_rate_history(self, symbol: str, limit: int = 100) -> list:
        try:
            data = self._public_get("/api/v1/market/funding-rate-history", {
                "instId": f"{symbol}-USDT",
                "limit": str(limit),
            })
            records = data.get("data", [])
            return [
                {"rate": float(r["fundingRate"]), "time": int(r.get("ts", r.get("fundingTime", 0)))}
                for r in records if r.get("fundingRate")
            ]
        except Exception:
            return []

    def get_funding_history(self, symbol: str, days: int = 7) -> list:
        records = self.get_funding_rate_history(symbol, limit=days * 3)
        return records[-days * 3:]

    @staticmethod
    def _ccxt_to_blofin_bar(interval: str) -> str:
        mapping = {
            "1m": "1m", "5m": "5m", "15m": "15m", "30m": "30m",
            "1h": "1H", "2h": "2H", "4h": "4H",
            "1d": "1D", "1w": "1W", "1M": "1M",
        }
        return mapping.get(interval, "1H")

    # ─────────────────────────────────────────────
    # Account
    # ─────────────────────────────────────────────

    def get_account_balance(self) -> float:
        data = self._private_get("/api/v1/account/balance", {"productType": "USDT-FUTURES"})
        balances = data.get("data", [])
        if isinstance(balances, dict):
            balances = [balances]
        for b in balances:
            if not isinstance(b, dict):
                continue
            details = b.get("details", [])
            for d in details:
                if d.get("currency") == "USDT":
                    return float(d.get("equity", d.get("eq", 0)) or 0)
        return 0.0

    def get_positions(self, inst_id: str = "") -> list:
        if self.trade_account == "copy":
            return self.get_copy_positions_normalized(inst_id)
        params = {}
        if inst_id:
            params["instId"] = inst_id
        data = self._private_get("/api/v1/account/positions", params)
        return data.get("data", [])

    def get_leverage_info(self, inst_id: str, margin_mode: str = "cross") -> dict:
        data = self._private_get("/api/v1/account/leverage-info", {
            "instId": inst_id,
            "mgnMode": margin_mode,
        })
        infos = data.get("data", [])
        return infos[0] if infos else {}

    def set_leverage(self, inst_id: str, leverage: str, margin_mode: str = "cross", pos_side: str = "") -> dict:
        if self.trade_account == "copy":
            return self._private_post("/api/v1/copytrading/account/set-leverage", {
                "instId": inst_id, "leverage": str(leverage),
                "marginMode": margin_mode, "positionSide": pos_side or "net",
            })
        body = {"instId": inst_id, "lever": leverage, "mgnMode": margin_mode}
        if pos_side:
            body["posSide"] = pos_side
        return self._private_post("/api/v1/account/set-leverage", body)

    def get_copy_positions(self, inst_id: str = "") -> list:
        params = {}
        if inst_id:
            params["instId"] = inst_id
        data = self._private_get("/api/v1/copytrading/account/positions-by-contract", params)
        out = data.get("data", [])
        return out if isinstance(out, list) else []

    def get_copy_positions_normalized(self, inst_id: str = "") -> list:
        """Copy positions mapped to the standard shape (instId/posSide/pos/avgPx/upl).""" 
        out = []
        for q in self.get_copy_positions(inst_id):
            try:
                qty = float(q.get("positions", 0) or 0)
            except (TypeError, ValueError):
                continue
            if qty <= 0:
                continue
            out.append({
                "instId": q.get("instId", ""),
                "posSide": str(q.get("positionSide", "net") or "net").lower(),
                "pos": qty,
                "avgPx": q.get("averagePrice", 0),
                "upl": q.get("unrealizedPnl", 0),
                "markPrice": q.get("markPrice", 0),
                "leverage": q.get("leverage", 0),
            })
        return out

    def get_copy_order_fill(self, order_id: str, tries: int = 10) -> dict:
        """Poll copy orders-history for a market fill (avg price / size / fee).""" 
        for _ in range(max(1, tries)):
            try:
                data = self._private_get("/api/v1/copytrading/trade/orders-history", {"orderId": order_id, "limit": "1"})
                items = data.get("data", [])
                if items:
                    o = items[0]
                    filled = float(o.get("filledSize", 0) or 0)
                    if filled > 0:
                        return {
                            "avg_px": float(o.get("averagePrice", 0) or 0),
                            "total_sz": filled,
                            "fee": float(o.get("fee", 0) or 0),
                            "oid": str(o.get("orderId", order_id)),
                        }
            except Exception:
                pass
            time.sleep(3)
        return {}

    # ─────────────────────────────────────────────
    # Order execution (live mode only)
    # ─────────────────────────────────────────────

    _lot_size_cache: dict = {}

    def quantize_size(self, inst_id: str, size: float, size_in_contracts: bool = False) -> str:
        """Floor size to contracts: size/contractValue unless already contracts,
        floored to lotSize. Copy API counts size in contracts."""
        try:
            lot, cv = self._lot_size_cache.get(inst_id, (None, None))
            if lot is None:
                data = self._public_get("/api/v1/market/instruments", {"instId": inst_id})
                items = data.get("data", [])
                lot = float(items[0].get("lotSize", "1") or "1") if items else 1.0
                cv = float(items[0].get("contractValue", "1") or "1") if items else 1.0
                if lot <= 0:
                    lot = 1.0
                if cv <= 0:
                    cv = 1.0
                self._lot_size_cache[inst_id] = (lot, cv)
            contracts = size if size_in_contracts else size / cv
            steps = int(contracts / lot + 1e-9)
            if steps <= 0:
                return ""
            q = steps * lot
            s = ("%.8f" % q).rstrip("0").rstrip(".")
            return s if s else ""
        except Exception:
            return str(size)

    def place_order(self, inst_id: str, margin_mode: str, side: str, order_type: str,
                    size: str, price: str = "", pos_side: str = "net",
                    reduce_only: bool = False, client_oid: str = "",
                    tp_trigger_px: str = "", tp_order_px: str = "",
                    sl_trigger_px: str = "", sl_order_px: str = "") -> dict:
        if self.trade_account == "copy":
            if pos_side not in ("long", "short"):
                raise RuntimeError("copy account is hedge mode: pos_side must be long/short, got %r" % (pos_side,))
            body = {"instId": inst_id, "marginMode": margin_mode, "positionSide": pos_side,
                    "side": side, "orderType": order_type, "size": str(size),
                    "brokerId": (os.environ.get("BLOFIN_BROKER_ID", "") or "")[:16]}
            if price:
                body["price"] = price
            if client_oid:
                body["clientOrderId"] = client_oid
            return self._private_post("/api/v1/copytrading/trade/place-order", body)
        body = {
            "instId": inst_id,
            "tdMode": margin_mode,
            "posSide": pos_side,
            "side": side,
            "ordType": order_type,
            "sz": size,
        }
        if price:
            body["px"] = price
        if reduce_only:
            body["reduceOnly"] = "true"
        if client_oid:
            body["clientOrderId"] = client_oid
        if tp_trigger_px:
            body["tpTriggerPx"] = tp_trigger_px
            body["tpOrdPx"] = tp_order_px or "-1"
        if sl_trigger_px:
            body["slTriggerPx"] = sl_trigger_px
            body["slOrdPx"] = sl_order_px or "-1"
        return self._private_post("/api/v1/trade/order", body)

    def close_position(self, inst_id: str, margin_mode: str, pos_side: str = "net",
                       client_oid: str = "") -> dict:
        body = {
            "instId": inst_id,
            "mgnMode": margin_mode,
            "posSide": pos_side,
        }
        if client_oid:
            body["clientOrderId"] = client_oid
        return self._private_post("/api/v1/trade/close-position", body)

    def market_open(self, symbol: str, is_buy: bool, size: float, inst_type: str = "swap", size_in_contracts: bool = False, pos_side_hint: str = "", is_close: bool = False) -> dict:
        if not self._is_live:
            raise RuntimeError(
                "market_open requires live mode (set BLOFIN_API_KEY, BLOFIN_API_SECRET, BLOFIN_PASSPHRASE)"
            )
        side = "buy" if is_buy else "sell"
        pos_side = "net"
        if self.trade_account == "copy":
            hint = (pos_side_hint or "").strip().lower()
            # Burzova pozicia je vzdy pravda - nacitaj ju prvu.
            exch = ""
            try:
                for q in self.get_copy_positions(f"{symbol}-USDT"):
                    if float(q.get("positions", 0) or 0) > 0:
                        exch = str(q.get("positionSide", "") or "").lower()
                        break
            except Exception:
                exch = ""
            if is_close and hint in ("long", "short"):
                # Close: burza musi mat rovnaku stranu, inak je DB zastarana.
                # Otocenie strany (open opposite) je zakazane - fail-closed.
                want_close = (hint == "long" and not is_buy) or (hint == "short" and is_buy)
                if want_close and exch != "" and exch != hint:
                    raise RuntimeError(f"DB says {hint} but exchange has {exch} for {symbol} - refusing (stale DB?)")
                if want_close and exch == "":
                    raise RuntimeError(f"DB says {hint} but exchange has no position for {symbol} - refusing (already closed?)")
                pos_side = hint
            elif is_close:
                # Close bez hintu: pouzi burzovu stranu, inak fail.
                if exch in ("long", "short"):
                    pos_side = exch
                else:
                    raise RuntimeError(f"SKIP: cannot determine position side for {symbol} (no hint, no exchange position) - refusing to open opposite side")
            else:
                if exch in ("long", "short"):
                    pos_side = exch
                else:
                    raise RuntimeError(f"SKIP: cannot determine position side for {symbol} (no hint, no exchange position) - refusing to open opposite side")
        inst_id = f"{symbol}-USDT"
        qsize = self.quantize_size(inst_id, float(size), size_in_contracts)
        cv = (self._lot_size_cache.get(inst_id, (None, None))[1] if isinstance(self._lot_size_cache.get(inst_id), tuple) else None) or 1.0
        if not qsize:
            raise RuntimeError(f"size {size} below lotSize for {inst_id}")
        result = self.place_order(
            inst_id=inst_id,
            margin_mode="cross",
            side=side,
            order_type="market",
            size=qsize,
            pos_side=pos_side,
        )
        try:
            if isinstance(result, dict):
                result["contract_value"] = cv
        except Exception:
            pass
        return result

    def market_close(self, symbol: str, sz: Optional[float] = None) -> dict:
        if not self._is_live:
            raise RuntimeError(
                "market_close requires live mode (set BLOFIN_API_KEY, BLOFIN_API_SECRET, BLOFIN_PASSPHRASE)"
            )
        inst_id = f"{symbol}-USDT"
        positions = self.get_positions(inst_id)
        pos_side = "net"
        pos_qty = 0.0
        for p in positions:
            if p.get("instId") == inst_id:
                qty = float(p.get("pos", 0) or 0)
                if qty > 0:
                    pos_side = p.get("posSide", "net")
                    pos_qty = qty
                break
        if pos_qty <= 0:
            return {}
        close_qty = pos_qty
        if sz is not None and sz > 0:
            close_qty = min(sz, pos_qty)
            if close_qty <= 0:
                return {}
        if self.trade_account == "copy":
            if pos_side not in ("long", "short"):
                raise RuntimeError("copy account is hedge mode: cannot close unknown side %r" % (pos_side,))
            return self._private_post("/api/v1/copytrading/trade/close-position-by-contract", {
                "instId": inst_id, "size": str(close_qty), "marginMode": "cross",
                "positionSide": pos_side, "closeType": "pnl",
            })
        close_side = "sell" if pos_side in ("long", "net") else "buy"
        result = self.place_order(
            inst_id=inst_id,
            margin_mode="cross",
            side=close_side,
            order_type="market",
            size=str(close_qty),
            pos_side=pos_side,
            reduce_only=True,
        )
        return result

    def cancel_order(self, inst_id: str, order_id: str = "", client_oid: str = "") -> dict:
        body = {"instId": inst_id}
        if order_id:
            body["ordId"] = order_id
        if client_oid:
            body["clientOrderId"] = client_oid
        return self._private_post("/api/v1/trade/cancel-order", body)

    def get_fills(self, inst_id: str = "", order_id: str = "", limit: str = "20") -> list:
        params = {}
        if inst_id:
            params["instId"] = inst_id
        if order_id:
            params["ordId"] = order_id
        params["limit"] = limit
        data = self._private_get("/api/v1/trade/fills-history", params)
        return data.get("data", [])
