#!/usr/bin/env python3
"""Fetch the BloFin account balance used by the dashboard's type filter."""
import json
import math
import os
import sys
import traceback
from datetime import datetime, timezone

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "platforms", "blofin"))


def parse_copy_perps_equity(data):
    if not isinstance(data, dict):
        raise RuntimeError("unexpected BloFin Copy Trading balance response")
    raw_total_equity = data.get("totalEquity")
    exchange_timestamp_ms = int(data.get("ts", 0) or 0)
    if raw_total_equity in (None, "") or exchange_timestamp_ms <= 0:
        raise RuntimeError("BloFin Copy Trading balance is missing totalEquity or ts")
    total_equity = float(raw_total_equity)
    if not math.isfinite(total_equity) or total_equity <= 0:
        raise RuntimeError(f"invalid BloFin Copy Trading totalEquity: {raw_total_equity!r}")
    details = data.get("details") or []
    available = sum(float(row.get("available", 0) or 0) for row in details)
    return total_equity, available, exchange_timestamp_ms


def main():
    try:
        from adapter import BloFinExchangeAdapter
        adapter = BloFinExchangeAdapter()
        if not adapter.is_live:
            raise RuntimeError("not live")

        mode = sys.argv[1] if len(sys.argv) > 1 else "all"
        if mode == "perps":
            data = adapter._private_get("/api/v1/copytrading/account/balance", {}).get("data", {})
            total_equity, available, exchange_timestamp_ms = parse_copy_perps_equity(data)
            account_type = "copy_trading_futures"
        elif mode in ("all", "spot"):
            account_type = "copy_trading" if mode == "all" else "spot"
            rows = adapter._private_get(
                "/api/v1/asset/balances", {"accountType": account_type}
            ).get("data", [])
            if not isinstance(rows, list):
                raise RuntimeError("unexpected BloFin asset balance response")
            # Current BloFin copy-trading and spot balances are USDT-denominated.
            # Count stablecoin balances at face value; don't add strategy-paper equity.
            stablecoins = {"USDT", "USDC", "USD"}
            total_equity = sum(
                float(row.get("balance", 0) or 0)
                for row in rows
                if str(row.get("currency", "")).upper() in stablecoins
            )
            available = sum(
                float(row.get("available", 0) or 0)
                for row in rows
                if str(row.get("currency", "")).upper() in stablecoins
            )
        else:
            raise ValueError("mode must be all, perps, or spot")

        print(json.dumps({
            "total_equity": total_equity,
            "available": available,
            "account_type": account_type,
            "mode": mode,
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "exchange_timestamp_ms": exchange_timestamp_ms if mode == "perps" else 0,
        }))
    except Exception as e:
        traceback.print_exc(file=sys.stderr)
        print(json.dumps({"error": str(e)}))
        sys.exit(1)


if __name__ == "__main__":
    main()
