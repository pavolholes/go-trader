from platforms.blofin.adapter import BloFinExchangeAdapter
from platforms.blofin.spot_adapter import BloFinSpotExchangeAdapter


def _candle_rows(timestamps):
    return [
        [str(ts), "1", "2", "0.5", "1.5", "10", "10", "15", "1"]
        for ts in timestamps
    ]


def _two_pages(params_seen):
    def fake_public_get(path, params=None):
        params_seen.append((path, dict(params or {})))
        if "after" not in params:
            return {"code": "0", "data": _candle_rows(range(4000, 2560, -1))}
        assert params["after"] == "2561"
        # Include the cursor row to exercise duplicate removal.
        return {"code": "0", "data": _candle_rows(range(2561, 1120, -1))}

    return fake_public_get


def test_perps_candle_fetch_pages_older_than_api_cap():
    adapter = BloFinExchangeAdapter()
    calls = []
    adapter._public_get = _two_pages(calls)

    rows = adapter.get_perp_ohlcv("BTC", "5m", 2000)

    assert len(rows) == 2000
    assert rows[0][0] == 2001
    assert rows[-1][0] == 4000
    assert len(calls) == 2
    assert calls[0][1]["limit"] == "1440"
    assert calls[1][1]["after"] == "2561"


def test_spot_candle_fetch_pages_older_than_api_cap():
    adapter = BloFinSpotExchangeAdapter()
    calls = []
    adapter._public_get = _two_pages(calls)

    rows = adapter.get_spot_ohlcv("BTC", "5m", 2000)

    assert len(rows) == 2000
    assert rows[0][0] == 2001
    assert rows[-1][0] == 4000
    assert len(calls) == 2
    assert calls[0][1]["instType"] == "SPOT"
    assert calls[1][1]["after"] == "2561"
