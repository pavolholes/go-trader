from __future__ import annotations

import pathlib
import sys

import pandas as pd

_BACKTEST = pathlib.Path(__file__).resolve().parents[1]
if str(_BACKTEST) not in sys.path:
    sys.path.insert(0, str(_BACKTEST))

from backtester import _position_anchored_avwap_from_prefix, _position_avwap_prefix


def _frame():
    return pd.DataFrame(
        {
            "high": [1000.0, 101.0, 202.0, 902.0],
            "low": [1000.0, 99.0, 198.0, 898.0],
            "close": [1000.0, 100.0, 200.0, 900.0],
            "volume": [100.0, 1.0, 3.0, 100.0],
        },
        index=pd.DatetimeIndex(
            [
                "2026-10-03 10:00",
                "2026-10-03 10:15",
                "2026-10-03 10:30",
                "2026-10-03 10:45",
            ],
            tz="UTC",
        ),
    )


def test_position_anchored_avwap_starts_at_first_bar_open_after_entry():
    prefix = _position_avwap_prefix(_frame())
    opened_at = pd.Timestamp("2026-10-03 10:07", tz="UTC")
    # At the close of the 10:30 bar, the 10:45 forming bar is excluded.
    assert _position_anchored_avwap_from_prefix(prefix, opened_at, 2) == 175.0


def test_position_anchored_avwap_at_exact_bar_open_includes_that_completed_bar():
    prefix = _position_avwap_prefix(_frame())
    opened_at = pd.Timestamp("2026-10-03 10:30", tz="UTC")
    assert _position_anchored_avwap_from_prefix(prefix, opened_at, 2) == 200.0


def test_position_anchored_avwap_returns_none_without_positive_volume():
    frame = _frame()
    frame["volume"] = 0.0
    assert _position_avwap_prefix(frame) is None
