#!/usr/bin/env python3
"""Fetch the BloFin account balance used by the dashboard's type filter."""
import json
import os
import sys
import traceback
from datetime import datetime, timezone

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "platforms", "blofin"))


def main():
    try:
        from adapter import BloFinExchangeAdapter
        adapter = BloFinExchangeAdapter()
        if not adapter.is_live:
            raise RuntimeError("not live")

        mode = sys.argv[1] if len(sys.argv) > 1 else "all"
        if mode == "perps":
            data = adapter._private_get("/api/v1/copytrading/account/balance", {}).get("data", {})
            details = data.get("details") or []
            total_equity = float(data.get("totalEquity", 0) or 0)
            available = sum(float(row.get("available", 0) or 0) for row in details)
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
        }))
    except Exception as e:
        traceback.print_exc(file=sys.stderr)
        print(json.dumps({"error": str(e)}))
        sys.exit(1)


if __name__ == "__main__":
    main()
