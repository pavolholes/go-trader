"""Closed-candle preparation for the ob_touch open strategy."""


def closed_ltf_frame(df, strategy_name: str, htf_timeframe: str = ""):
    """Drop the latest LTF candle for MTF ob_touch checks.

    Exchange OHLCV endpoints can include a currently forming candle, and the
    six-column normalized adapter shape no longer carries BloFin's ``confirm``
    flag. Dropping the trailing LTF candle is a conservative common rule for
    paper/live signal checks. Backtests are unaffected and continue to evaluate
    every historical candle. HTF frames are independently trimmed in
    ``ob_touch_core``.
    """
    if (strategy_name or "").strip() != "ob_touch" or not htf_timeframe:
        return df
    if df is None or len(df) < 2:
        return df
    return df.iloc[:-1].copy()
