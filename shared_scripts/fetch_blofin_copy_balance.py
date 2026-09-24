#!/usr/bin/env python3
"""BloFin copy-trading account balance (live leader account)."""
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
        d = adapter._private_get("/api/v1/copytrading/account/balance", {})
        data = d.get("data", {})
        det = (data.get("details") or [{}])[0]
        print(json.dumps({
            "total_equity": float(data.get("totalEquity", 0) or 0),
            "available": float(det.get("available", 0) or 0),
            "timestamp": datetime.now(timezone.utc).isoformat(),
        }))
    except Exception as e:
        traceback.print_exc(file=sys.stderr)
        print(json.dumps({"error": str(e)}))
        sys.exit(1)


if __name__ == "__main__":
    main()
