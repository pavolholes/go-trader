import numpy as np

from shared_strategies.open.conftest import load_module, make_ohlcv

_BREAKOUT_RETEST = load_module(
    '_breakout_retest_test',
    __file__.replace('test_breakout_retest.py', 'breakout_retest.py'),
)
breakout_retest_core = _BREAKOUT_RETEST.breakout_retest_core


def _frame(side='long', retest=True, breakout_volume=500.0):
    n = 90
    close = np.full(n, 100.0)
    open_px = close.copy()
    high = close + 0.10
    low = close - 0.10
    volume = np.full(n, 100.0)

    if side == 'long':
        open_px[70], close[70], high[70], low[70] = 100.0, 101.0, 101.1, 99.95
        if retest:
            open_px[71], close[71], high[71], low[71] = 100.2, 100.5, 100.7, 100.05
        else:
            open_px[71:76] = 101.0
            close[71:76] = 101.2
            high[71:76] = 101.3
            low[71:76] = 101.0
    else:
        open_px[70], close[70], high[70], low[70] = 100.0, 99.0, 100.05, 98.9
        if retest:
            open_px[71], close[71], high[71], low[71] = 99.8, 99.5, 99.95, 99.4
        else:
            open_px[71:76] = 98.9
            close[71:76] = 98.7
            high[71:76] = 99.0
            low[71:76] = 98.6
    volume[70] = breakout_volume
    return make_ohlcv(close, volume=volume, opens=open_px, highs=high, lows=low)


def _params(**updates):
    params = {
        'channel_lookback': 20,
        'atr_period': 5,
        'compression_lookback': 20,
        'compression_quantile': 0.30,
        'volume_window': 10,
        'volume_multiplier': 1.5,
        'retest_window': 3,
        'retest_atr_buffer': 0.5,
    }
    params.update(updates)
    return params


def test_long_entry_waits_for_breakout_retest_and_reclaim():
    out = breakout_retest_core(_frame(), **_params())
    assert out['signal'].iloc[70] == 0
    assert out['signal'].iloc[71] == 1


def test_breakout_without_retest_never_enters():
    out = breakout_retest_core(_frame(retest=False), **_params())
    assert not (out['signal'] != 0).any()


def test_low_volume_breakout_is_ignored():
    out = breakout_retest_core(_frame(breakout_volume=100.0), **_params())
    assert not (out['signal'] != 0).any()


def test_short_retest_requires_short_permission():
    frame = _frame(side='short')
    blocked = breakout_retest_core(frame, **_params(allow_short=False))
    allowed = breakout_retest_core(frame, **_params(allow_short=True))
    assert not (blocked['signal'] != 0).any()
    assert allowed['signal'].iloc[71] == -1


def test_future_bars_do_not_change_prior_signals():
    frame = _frame()
    full = breakout_retest_core(frame, **_params())
    prefix = breakout_retest_core(frame.iloc[:71], **_params())
    assert np.array_equal(full['signal'].iloc[:71].to_numpy(), prefix['signal'].to_numpy())
