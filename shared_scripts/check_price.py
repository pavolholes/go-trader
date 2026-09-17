#!/usr/bin/env python3

import sys
import json
import traceback


def main():
    symbols = sys.argv[1:]
    if not symbols:
        print(json.dumps({}))
        return

    try:
        import ccxt
        exchange = ccxt.binanceus({"enableRateLimit": True})

        prices = {}
        for symbol in symbols:
            try:
                ticker = exchange.fetch_ticker(symbol)
                prices[symbol] = round(ticker["last"], 2)
            except Exception as e:
                print(f"Failed to fetch {symbol}: {e}", file=sys.stderr)

        missing = [s for s in symbols if s not in prices]
        if missing:
            try:
                import os as _os2
                sys.path.insert(0, _os2.path.join(_os2.path.dirname(_os2.path.abspath(__file__)), "..", "platforms", "blofin"))
                from spot_adapter import BloFinSpotExchangeAdapter
                tickers = {x.get("instId"): x for x in BloFinSpotExchangeAdapter().get_spot_tickers()}
                for symbol in missing:
                    base = (symbol or "").split("/")[0].split("-")[0].strip().upper()
                    tk = tickers.get(base + "-USDT", {})
                    try:
                        last = float(tk.get("last", 0) or 0)
                    except (ValueError, TypeError):
                        last = 0
                    if last > 0:
                        prices[symbol] = round(last, 2)
            except Exception as e2:
                print(f"BloFin spot fallback failed: {e2}", file=sys.stderr)

        print(json.dumps(prices))

    except Exception as e:
        traceback.print_exc(file=sys.stderr)
        print(json.dumps({}))
        sys.exit(1)


if __name__ == "__main__":
    main()
