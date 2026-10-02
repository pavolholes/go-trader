
import importlib.util
import os
import sys
import types

import pandas as pd

_HERE = os.path.dirname(os.path.abspath(__file__))


def _load_adapter():
    spec = importlib.util.spec_from_file_location(
        "_topstep_adapter_retry_test", os.path.join(_HERE, "adapter.py")
    )
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def _frame(n=10):
    idx = pd.date_range("2026-01-01", periods=n, freq="15min", tz="UTC")
    return pd.DataFrame(
        {"Open": 100.0, "High": 101.0, "Low": 99.0, "Close": 100.5,
         "Volume": 1000.0},
        index=idx,
    )


class _Ticker:
    def __init__(self, script, history_calls=None):
        self._script = script
        self._history_calls = history_calls if history_calls is not None else []

    def history(self, period=None, interval=None):
        self._history_calls.append((period, interval))
        action = self._script.pop(0)
        if isinstance(action, Exception):
            raise action
        return action


def _install_fake_yfinance(monkeypatch, script, calls, history_calls=None):
    fake = types.ModuleType("yfinance")
    fake.Ticker = lambda sym: (calls.append(sym), _Ticker(script, history_calls))[1]
    monkeypatch.setitem(sys.modules, "yfinance", fake)


def _install_chart_error(monkeypatch):
    fake = types.ModuleType("requests")

    def get(*args, **kwargs):
        raise RuntimeError("chart endpoint unavailable")

    fake.get = get
    monkeypatch.setitem(sys.modules, "requests", fake)


class _ChartResponse:
    def raise_for_status(self):
        return None

    def json(self):
        return {
            "chart": {
                "result": [{
                    "timestamp": [1_700_000_000, 1_700_000_900, 1_700_001_800],
                    "indicators": {"quote": [{
                        "open": [100.0, None, 102.0],
                        "high": [101.0, None, 103.0],
                        "low": [99.0, None, 101.0],
                        "close": [100.5, None, 102.5],
                        "volume": [1000.0, None, 1200.0],
                    }]},
                }],
                "error": None,
            }
        }


def test_retry_rides_out_four_transient_failures(monkeypatch):
    mod = _load_adapter()
    _install_chart_error(monkeypatch)
    calls = []
    sleeps = []
    monkeypatch.setattr(mod.time, "sleep", lambda s: sleeps.append(s))
    _install_fake_yfinance(
        monkeypatch,
        [RuntimeError("boom")] * 4 + [_frame()],
        calls,
    )
    adapter = mod.TopStepExchangeAdapter(mode="paper")
    out = adapter._get_yahoo_ohlcv("NQ", "15m", 200)
    assert len(out) == 10
    assert calls == ["NQ=F"] * 5
    assert sleeps == [1, 2, 3, 3]


def test_persistent_outage_returns_empty_after_five_attempts(monkeypatch):
    mod = _load_adapter()
    _install_chart_error(monkeypatch)
    sleeps = []
    monkeypatch.setattr(mod.time, "sleep", lambda s: sleeps.append(s))
    _install_fake_yfinance(
        monkeypatch,
        [RuntimeError("down")] * 5,
        [],
    )
    adapter = mod.TopStepExchangeAdapter(mode="paper")
    assert adapter._get_yahoo_ohlcv("NQ", "15m", 200) == []
    assert sleeps == [1, 2, 3, 3]


def test_unknown_symbol_short_circuits():
    mod = _load_adapter()
    adapter = mod.TopStepExchangeAdapter(mode="paper")
    assert adapter._get_yahoo_ohlcv("NOPE", "15m", 200) == []


def test_supported_intraday_periods_fetch_enough_swing_history(monkeypatch):
    mod = _load_adapter()
    _install_chart_error(monkeypatch)
    periods = []
    adapter = mod.TopStepExchangeAdapter(mode="paper")
    for interval, expected in (("5m", "60d"), ("15m", "60d"), ("1m", "7d")):
        history_calls = []
        _install_fake_yfinance(
            monkeypatch, [_frame()], [], history_calls=history_calls
        )
        assert adapter._get_yahoo_ohlcv("NQ", interval, 600)
        periods.append(history_calls[0])
        assert history_calls[0][0] == expected
        monkeypatch.delitem(sys.modules, "yfinance")
    assert periods == [("60d", "5m"), ("60d", "15m"), ("7d", "1m")]


def test_public_yahoo_chart_fallback_maps_rows_without_cookie_client(monkeypatch):
    mod = _load_adapter()
    request_calls = []
    fake = types.ModuleType("requests")
    fake.get = lambda *args, **kwargs: (request_calls.append((args, kwargs)), _ChartResponse())[1]
    monkeypatch.setitem(sys.modules, "requests", fake)

    adapter = mod.TopStepExchangeAdapter(mode="paper")
    rows = adapter._get_yahoo_ohlcv("NQ", "15m", 10)

    assert rows == [
        [1_700_000_000_000, 100.0, 101.0, 99.0, 100.5, 1000.0],
        [1_700_001_800_000, 102.0, 103.0, 101.0, 102.5, 1200.0],
    ]
    args, kwargs = request_calls[0]
    assert args[0].endswith("/NQ=F")
    assert kwargs["params"]["interval"] == "15m"
    assert kwargs["headers"]["Origin"] == "https://finance.yahoo.com"
