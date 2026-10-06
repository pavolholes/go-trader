from __future__ import annotations

import importlib.util
from pathlib import Path

import pandas as pd
import pytest

_PATH = Path(__file__).with_name("data_fetcher.py")
_SPEC = importlib.util.spec_from_file_location("_data_fetcher_cache_coverage", _PATH)
_DATA = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(_DATA)


def _frame(timestamps):
    return pd.DataFrame(
        {
            "timestamp": timestamps,
            "open": [100.0] * len(timestamps),
            "high": [101.0] * len(timestamps),
            "low": [99.0] * len(timestamps),
            "close": [100.0] * len(timestamps),
            "volume": [1.0] * len(timestamps),
        },
        index=pd.to_datetime(timestamps, unit="ms"),
    )


def test_nonempty_stale_cache_fetches_missing_tail_without_writing_when_store_false(monkeypatch):
    now_ms = int(pd.Timestamp("2026-10-03 12:30:00", tz="UTC").timestamp() * 1000)
    old = int(pd.Timestamp("2026-06-18 10:00:00", tz="UTC").timestamp() * 1000)
    latest_open = (now_ms // 3_600_000 - 1) * 3_600_000
    fetched = list(range(old + 3_600_000, latest_open + 1, 3_600_000))
    calls = []
    monkeypatch.setattr(_DATA.time, "time", lambda: now_ms / 1000)
    monkeypatch.setattr(_DATA, "load_ohlcv", lambda *a, **kw: _frame([old]))

    def refresh(symbol, timeframe, since, exchange_id, store):
        calls.append((since, store))
        return _frame(fetched)

    monkeypatch.setattr(_DATA, "fetch_full_history", refresh)
    got = _DATA.load_cached_data(
        "BTC/USDT", "1h", exchange_id="binanceus", start_date="2026-06-18 10:00", store=False
    )
    assert calls and calls[0][1] is False
    assert len(got) == len(fetched) + 1
    assert int(got["timestamp"].iloc[-1]) == fetched[-1]


def test_stale_nonempty_cache_fails_loudly_when_tail_refresh_is_empty(monkeypatch):
    now_ms = int(pd.Timestamp("2026-10-03 12:30:00", tz="UTC").timestamp() * 1000)
    old = int(pd.Timestamp("2026-06-18 10:00:00", tz="UTC").timestamp() * 1000)
    monkeypatch.setattr(_DATA.time, "time", lambda: now_ms / 1000)
    monkeypatch.setattr(_DATA, "load_ohlcv", lambda *a, **kw: _frame([old]))
    monkeypatch.setattr(_DATA, "fetch_full_history", lambda *a, **kw: _frame([]))
    with pytest.raises(ValueError, match="stale OHLCV range.*does not cover requested period"):
        _DATA.load_cached_data("BTC/USDT", "1h", exchange_id="binanceus", store=False)


def test_fresh_cache_does_not_fetch(monkeypatch):
    now_ms = int(pd.Timestamp("2026-10-03 12:30:00", tz="UTC").timestamp() * 1000)
    fresh = [now_ms - 2 * 3_600_000]
    monkeypatch.setattr(_DATA.time, "time", lambda: now_ms / 1000)
    monkeypatch.setattr(_DATA, "load_ohlcv", lambda *a, **kw: _frame(fresh))
    monkeypatch.setattr(_DATA, "fetch_full_history", lambda *a, **kw: pytest.fail("unexpected refresh"))
    got = _DATA.load_cached_data("BTC/USDT", "1h", exchange_id="binanceus", store=False)
    assert len(got) == 1
