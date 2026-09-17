"""Export top BloFin spot symbols ranked by 24h USDT volume.

Ranking uses USDT volume = volCurrency24h (base volume) * last price,
NOT raw base volume (that favours cheap meme tokens).

Output: JSON with per-category ranking + suggested phase-1 set
(top ~12 by USDT volume, at least 1 per available category).

Usage (on host):
    python3 shared_scripts/export_spot_top.py [--out /tmp/spot_top.json]
"""

import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                "..", "platforms", "blofin"))

CATEGORIES = {
    "TradFi": ["XAUT", "WLFI", "GOOGLX", "AMZNX", "AAPLX", "COINX",
               "SPCXX", "NVDAX", "TSLAX", "METAX"],
    "AI": ["DEXE", "VVV", "SAHARA", "VIRTUAL"],
    "Solana": ["PENGU", "PYTH", "TNSR"],
    "Payment": ["ACH"],
    "Infrastructure": ["COTI", "ENS", "JASMY", "PYTH", "ACH"],
    "Meme": ["PEPE", "DOGE", "TRUMP", "PEOPLE", "ORDI", "SHIB",
             "MET", "BONK"],
    "Layer1": ["ETH", "SOL", "NEAR", "APT", "BNB", "ZEC", "ADA",
               "SUI", "AVAX"],
    "Layer2": ["ARB", "OP", "MNT", "ZRX"],
    "DeFi": ["ENA", "WBTC", "AVAX", "RAIN", "UNI"],
    "GameFi": ["ICP", "GALA", "AXS", "FLOKI", "GMT"],
    "Depin": ["RENDER", "FIL"],
    "NFT": ["ICP", "GALA", "SHIB", "FET", "AXS"],
}


def main() -> None:
    from spot_adapter import BloFinSpotExchangeAdapter

    out_path = "/tmp/spot_top.json"
    for i, a in enumerate(sys.argv[1:]):
        if a == "--out" and i + 1 < len(sys.argv[1:]):
            out_path = sys.argv[1:][i + 1]

    adapter = BloFinSpotExchangeAdapter()
    tickers = adapter.get_spot_tickers()

    stats = {}
    for t in tickers:
        try:
            base = str(t.get("instId", "")).replace("-USDT", "")
            last = float(t.get("last", 0) or 0)
            vol_base = float(t.get("volCurrency24h", 0) or 0)
        except (ValueError, TypeError):
            continue
        if last <= 0:
            continue
        stats[base] = {"last": last, "vol_base": vol_base,
                       "vol_usdt": vol_base * last}

    result = {"categories": {}, "phase1": []}
    seen = set()
    for cat, syms in CATEGORIES.items():
        ranked = sorted(
            ((s, stats.get(s, {}).get("vol_usdt", 0)) for s in syms),
            key=lambda x: -x[1],
        )
        result["categories"][cat] = [
            {"symbol": s, "vol_usdt": v} for s, v in ranked
        ]
        if ranked and ranked[0][1] > 0:
            seen.add(ranked[0][0])

    # Phase-1 set: category winners first, then fill by global USDT volume.
    phase1 = list(seen)
    rest = sorted(
        ((s, v.get("vol_usdt", 0)) for s, v in stats.items() if s not in seen),
        key=lambda x: -x[1],
    )
    for s, v in rest:
        if len(phase1) >= 12:
            break
        if v > 0:
            phase1.append(s)
    result["phase1"] = phase1[:12]

    with open(out_path, "w") as f:
        json.dump(result, f, indent=1)
    print(json.dumps(result, indent=1))


if __name__ == "__main__":
    main()
