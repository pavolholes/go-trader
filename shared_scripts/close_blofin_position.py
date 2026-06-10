#!/usr/bin/env python3
"""
BloFin emergency position close script.

Submits a reduce-only market close for a single coin via the BloFin adapter's
``market_close``. Used by the portfolio kill switch in the Go scheduler.

Usage:
    close_blofin_position.py --symbol=BTC --mode=live
    close_blofin_position.py --symbol=BTC --mode=live --sz=1.0
"""

import argparse
import json
import sys
import traceback
from datetime import datetime, timezone

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "platforms", "blofin"))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--symbol", required=True)
    parser.add_argument("--mode", default="live")
    parser.add_argument("--sz", type=float, default=None)
    args = parser.parse_args()

    if args.mode != "live":
        print(json.dumps({
            "close": None,
            "platform": "blofin",
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "error": "--mode=live required for emergency close",
        }))
        sys.exit(1)

    try:
        from adapter import BloFinExchangeAdapter
        adapter = BloFinExchangeAdapter()
        if not adapter.is_live:
            _emit_error(args.symbol, "BloFin adapter not live — set BLOFIN_API_KEY / BLOFIN_API_SECRET / BLOFIN_PASSPHRASE")
            return
        result = adapter.market_close(args.symbol, args.sz)
    except Exception as e:
        traceback.print_exc(file=sys.stderr)
        _emit_error(args.symbol, str(e))
        return

    if not isinstance(result, dict):
        _emit_error(args.symbol, f"unexpected adapter response type {type(result).__name__}: {result!r}")
        return

    if not result:
        _emit_success(args.symbol, fill={}, already_flat=True)
        return

    fill = {}
    try:
        data = result.get("data", [{}])[0] if result.get("data") else result
        avg = float(data.get("fillPx", 0) or 0) or float(data.get("avgPx", 0) or 0)
        filled = float(data.get("fillSz", 0) or 0) or float(data.get("accFillSz", 0) or 0)
        if avg:
            fill["avg_px"] = avg
        if filled:
            fill["total_sz"] = filled
        oid = data.get("ordId") or result.get("ordId", "")
        if oid:
            fill["oid"] = str(oid)
    except (TypeError, ValueError):
        pass

    _emit_success(args.symbol, fill)


def _emit_success(symbol, fill, already_flat=False):
    close = {"symbol": symbol, "fill": fill}
    if already_flat:
        close["already_flat"] = True
    print(json.dumps({
        "close": close,
        "platform": "blofin",
        "timestamp": datetime.now(timezone.utc).isoformat(),
    }))


def _emit_error(symbol, message):
    print(json.dumps({
        "close": {"symbol": symbol, "fill": {}},
        "platform": "blofin",
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "error": message,
    }))
    sys.exit(1)


if __name__ == "__main__":
    main()
