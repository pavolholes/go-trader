#!/usr/bin/env python3
"""
BloFin account balance fetcher.

Fetches total USDT equity from the BloFin futures account and emits to stdout.
Used by the Go scheduler for portfolio-level equity tracking.

Usage:
    fetch_blofin_balance.py

Output:
    {"balance": 1234.56, "platform": "blofin", "timestamp": ...}
"""

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
            _emit_error("BloFin adapter not live — set BLOFIN_API_KEY / BLOFIN_API_SECRET / BLOFIN_PASSPHRASE")
            return
        balance = adapter.get_account_balance()
    except Exception as e:
        traceback.print_exc(file=sys.stderr)
        _emit_error(str(e))
        return

    print(json.dumps({
        "balance": balance,
        "platform": "blofin",
        "timestamp": datetime.now(timezone.utc).isoformat(),
    }))


def _emit_error(message):
    print(json.dumps({
        "balance": 0.0,
        "platform": "blofin",
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "error": message,
    }))
    sys.exit(1)


if __name__ == "__main__":
    main()
