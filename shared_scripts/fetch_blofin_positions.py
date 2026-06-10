#!/usr/bin/env python3
"""
BloFin live positions fetcher.

Fetches every open BloFin perpetual swap position and emits a JSON list to stdout.
Used by the portfolio kill switch in the Go scheduler.

Usage:
    fetch_blofin_positions.py

Output:
    {"positions": [{"coin": "BTC", "size": 0.334, "entry_price": 42000.5,
      "side": "long", "unrealized_pnl": 12.5}, ...],
     "platform": "blofin", "timestamp": ...}
"""

import json
import sys
import traceback
from datetime import datetime, timezone

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "platforms", "blofin"))


def main():
    try:
        from adapter import BloFinExchangeAdapter
        adapter = BloFinExchangeAdapter()
        if not adapter.is_live:
            _emit_error("BloFin adapter not live — set BLOFIN_API_KEY / BLOFIN_API_SECRET / BLOFIN_PASSPHRASE")
            return
        raw = adapter.get_positions()
    except Exception as e:
        traceback.print_exc(file=sys.stderr)
        _emit_error(str(e))
        return

    positions = []
    for p in raw or []:
        try:
            pos = float(p.get("pos") or 0)
        except (TypeError, ValueError):
            continue
        if pos == 0:
            continue
        inst_id = p.get("instId", "")
        coin = inst_id.split("-", 1)[0] if inst_id else ""
        if not coin:
            continue
        side = (p.get("posSide") or "net").lower()
        signed_size = -pos if side == "short" else pos
        entry_price = 0.0
        try:
            entry_price = float(p.get("avgPx") or 0)
        except (TypeError, ValueError):
            pass
        unrealized_pnl = 0.0
        try:
            unrealized_pnl = float(p.get("upl") or 0)
        except (TypeError, ValueError):
            pass
        positions.append({
            "coin": coin,
            "size": signed_size,
            "entry_price": entry_price,
            "side": side,
            "unrealized_pnl": unrealized_pnl,
        })

    print(json.dumps({
        "positions": positions,
        "platform": "blofin",
        "timestamp": datetime.now(timezone.utc).isoformat(),
    }))


def _emit_error(message):
    print(json.dumps({
        "positions": [],
        "platform": "blofin",
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "error": message,
    }))
    sys.exit(1)


if __name__ == "__main__":
    main()
