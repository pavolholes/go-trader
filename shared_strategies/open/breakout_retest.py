import numpy as np
import pandas as pd

from indicators_core import atr_sma_series


def breakout_retest_core(
    df: pd.DataFrame,
    channel_lookback: int = 20,
    atr_period: int = 14,
    compression_lookback: int = 60,
    compression_quantile: float = 0.30,
    volume_window: int = 20,
    volume_multiplier: float = 1.5,
    retest_window: int = 5,
    retest_atr_buffer: float = 0.35,
    allow_short: bool = False,
) -> pd.DataFrame:
    positive = (channel_lookback, atr_period, compression_lookback, volume_window, retest_window)
    if any(int(value) < 1 for value in positive):
        raise ValueError('lookbacks and retest_window must be positive')
    if not 0.0 < float(compression_quantile) < 1.0:
        raise ValueError('compression_quantile must be between zero and one')
    if float(volume_multiplier) < 0.0 or float(retest_atr_buffer) < 0.0:
        raise ValueError('volume_multiplier and retest_atr_buffer must be non-negative')

    result = df.copy()
    n = len(result)
    result['signal'] = np.zeros(n, dtype=np.int64)
    if n == 0:
        return result

    close = pd.to_numeric(result['close'], errors='coerce').astype(float)
    high = pd.to_numeric(result['high'], errors='coerce').astype(float)
    low = pd.to_numeric(result['low'], errors='coerce').astype(float)
    open_px = (
        pd.to_numeric(result['open'], errors='coerce').astype(float)
        if 'open' in result.columns else close
    )
    volume = (
        pd.to_numeric(result['volume'], errors='coerce').astype(float)
        if 'volume' in result.columns else pd.Series(np.nan, index=result.index)
    )

    atr = pd.Series(
        atr_sma_series(high, low, close, atr_period),
        index=result.index,
        dtype=float,
    )
    atr_pct = (atr / close.replace(0.0, np.nan)).replace([np.inf, -np.inf], np.nan)
    prior_atr_pct = atr_pct.shift(1)
    compression_floor = prior_atr_pct.rolling(
        window=compression_lookback,
        min_periods=compression_lookback,
    ).quantile(float(compression_quantile))
    compressed = (prior_atr_pct <= compression_floor).fillna(False)

    channel_high = high.shift(1).rolling(
        window=channel_lookback,
        min_periods=channel_lookback,
    ).max()
    channel_low = low.shift(1).rolling(
        window=channel_lookback,
        min_periods=channel_lookback,
    ).min()
    volume_baseline = volume.shift(1).rolling(
        window=volume_window,
        min_periods=volume_window,
    ).mean()
    if float(volume_multiplier) == 0.0:
        volume_expansion = pd.Series(True, index=result.index)
    else:
        volume_expansion = (volume >= volume_baseline * float(volume_multiplier)).fillna(False)

    prior_close = close.shift(1)
    long_break = (
        (close > channel_high)
        & (prior_close <= channel_high.shift(1))
        & compressed
        & volume_expansion
    ).fillna(False).to_numpy(dtype=bool)
    short_break = (
        (close < channel_low)
        & (prior_close >= channel_low.shift(1))
        & compressed
        & volume_expansion
    ).fillna(False).to_numpy(dtype=bool)

    close_values = close.to_numpy(dtype=float)
    open_values = open_px.to_numpy(dtype=float)
    high_values = high.to_numpy(dtype=float)
    low_values = low.to_numpy(dtype=float)
    atr_values = atr.to_numpy(dtype=float)
    upper_values = channel_high.to_numpy(dtype=float)
    lower_values = channel_low.to_numpy(dtype=float)
    signals = np.zeros(n, dtype=np.int64)
    pending_sides = np.zeros(n, dtype=np.int64)
    pending_levels = np.full(n, np.nan, dtype=float)
    pending_side = 0
    pending_level = np.nan
    breakout_bar = -1

    for i in range(n):
        if pending_side:
            age = i - breakout_bar
            atr_now = atr_values[i]
            tolerance = (
                float(retest_atr_buffer) * max(atr_now, 0.0)
                if np.isfinite(atr_now) else 0.0
            )
            if age > retest_window:
                pending_side = 0
            elif age > 0 and pending_side == 1:
                if close_values[i] < pending_level - tolerance:
                    pending_side = 0
                elif (
                    low_values[i] <= pending_level + tolerance
                    and high_values[i] >= pending_level - tolerance
                    and close_values[i] >= pending_level
                    and close_values[i] > open_values[i]
                ):
                    signals[i] = 1
                    pending_side = 0
            elif age > 0 and pending_side == -1:
                if close_values[i] > pending_level + tolerance:
                    pending_side = 0
                elif (
                    high_values[i] >= pending_level - tolerance
                    and low_values[i] <= pending_level + tolerance
                    and close_values[i] <= pending_level
                    and close_values[i] < open_values[i]
                ):
                    signals[i] = -1
                    pending_side = 0

            if signals[i] != 0:
                continue
            if pending_side:
                pending_sides[i] = pending_side
                pending_levels[i] = pending_level
                continue

        if long_break[i]:
            pending_side = 1
            pending_level = upper_values[i]
            breakout_bar = i
        elif allow_short and short_break[i]:
            pending_side = -1
            pending_level = lower_values[i]
            breakout_bar = i

        if pending_side:
            pending_sides[i] = pending_side
            pending_levels[i] = pending_level

    result['atr'] = atr
    result['channel_high'] = channel_high
    result['channel_low'] = channel_low
    result['volatility_compressed'] = compressed
    result['volume_expansion'] = volume_expansion
    result['pending_side'] = pending_sides
    result['pending_level'] = pending_levels
    result['signal'] = signals
    return result
