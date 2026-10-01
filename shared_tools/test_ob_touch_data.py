import pandas as pd

from shared_tools.ob_touch_data import closed_ltf_frame


def test_ob_touch_mtf_drops_only_trailing_ltf_candle():
    df = pd.DataFrame({"close": [1, 2, 3, 4]})
    out = closed_ltf_frame(df, "ob_touch", "1h")
    assert out["close"].tolist() == [1, 2, 3]
    assert df["close"].tolist() == [1, 2, 3, 4]


def test_single_timeframe_or_other_strategy_keeps_full_frame():
    df = pd.DataFrame({"close": [1, 2, 3]})
    assert len(closed_ltf_frame(df, "ob_touch", "")) == 3
    assert len(closed_ltf_frame(df, "rsi", "1h")) == 3


def test_short_frame_is_returned_unchanged():
    df = pd.DataFrame({"close": [1]})
    assert closed_ltf_frame(df, "ob_touch", "1h") is df


def test_none_frame_is_safe():
    assert closed_ltf_frame(None, "ob_touch", "1h") is None
