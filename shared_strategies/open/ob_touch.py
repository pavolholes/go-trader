"""OB Touch — SMC order-block touch entries with higher-timeframe confirmation.

Port of the TradingView "Institutional Level" (INL) indicator, entry part only
(structure breaks + institutional zones + wick touch; display-only volume
metrics are intentionally not ported).

Signal logic (all evaluated on CLOSED bars, no future-peek):
- Internal pivots (``internal_lookback``) feed BOS/CHoCH breaks. Every break
  creates an institutional zone from the bars between the last confirmed
  pivot and the break bar (``Precise`` positioning: the zone spans the
  extreme low/high to the hl2 of the extreme candle, with the Pine "Precise"
  tightening applied).
- Swing pivots (``swing_lookback``) feed the swing trend used as an optional
  directional filter (``use_swing_filter``) and, when ``use_swing_levels`` is
  true, a second set of institutional zones.
- Zones die by ``Absolute`` mitigation: a bullish zone dies on a close below
  its bottom, a bearish zone on a close above its top. A new zone that
  overlaps the previous same-side zone is discarded (``Previous`` overlap
  rule).
- Entry: the FIRST bar whose wick (``touch_mode="wick"``) or close
  (``touch_mode="close"``) reaches the overlap of an active LTF zone and an
  active HTF zone of the same side. Long = +1, short = -1 (suppressed when
  ``allow_short`` is false, e.g. the spot variant).

Higher-timeframe confirmation (in-frame resampling, same convention as
``mtf_confluence`` — only completed HTF candles are ever visible):

    ``htf_factor`` = how many native bars form one HTF candle.

    +--------------+------------------+---------------------------+
    | Native (LTF) | htf_factor       | Confirming TF (HTF)       |
    +==============+==================+===========================+
    | 5m           | 12               | 1H (twelve 5m bars)       |
    +--------------+------------------+---------------------------+
    | 15m          | 4                | 1H (four 15m bars)        |
    +--------------+------------------+---------------------------+
    | 5m           | 3                | 15m (three 5m bars)       |
    +--------------+------------------+---------------------------+
    | any          | 1                | single-TF mode (no gate)  |
    +--------------+------------------+---------------------------+

When the history is too short to confirm even one HTF pivot the strategy
stays flat (fail-closed). Deep HTF history (e.g. ~120+ HTF candles for the
default swing lookback of 50) must be provided by the caller via a larger
OHLCV fetch, not by this function.
"""

import numpy as np
import pandas as pd


_HTF_AGG = {"open": "first", "high": "max", "low": "min", "close": "last"}

_VALID_TOUCH_MODES = ("wick", "close")


def _resample_htf(df: pd.DataFrame, htf_factor: int):
    """Resample native bars into completed HTF candles.

    Returns (htf_frame, visible_labels): ``visible_labels[k]`` is the native
    label of the LAST native bar of HTF candle ``k``, i.e. the first native
    bar at which candle ``k`` is fully closed and may be used.
    Same convention as ``mtf_confluence._resample_htf``.
    """
    native_td = None
    if isinstance(df.index, pd.DatetimeIndex) and len(df) >= 3:
        diffs = df.index.to_series().diff().dropna()
        if len(diffs) > 0:
            cadence = diffs.mode()
            if len(cadence) > 0 and cadence.iloc[0] > pd.Timedelta(0):
                native_td = cadence.iloc[0]

    if native_td is not None:
        htf_td = native_td * htf_factor
        htf = (
            df[["open", "high", "low", "close"]]
            .resample(htf_td, label="left", closed="left", origin="epoch")
            .agg(_HTF_AGG)
            .dropna(subset=["close"])
        )
        visible_at = htf.index + htf_td - native_td
        return htf, visible_at

    n = len(df)
    n_full = n // htf_factor
    if n_full == 0:
        empty = pd.DataFrame(columns=list(_HTF_AGG))
        return empty, df.index[:0]
    o = df["open"].to_numpy(dtype=float)[: n_full * htf_factor].reshape(n_full, htf_factor)
    h = df["high"].to_numpy(dtype=float)[: n_full * htf_factor].reshape(n_full, htf_factor)
    l = df["low"].to_numpy(dtype=float)[: n_full * htf_factor].reshape(n_full, htf_factor)
    c = df["close"].to_numpy(dtype=float)[: n_full * htf_factor].reshape(n_full, htf_factor)
    last_pos = np.arange(1, n_full + 1) * htf_factor - 1
    visible_at = df.index[last_pos]
    htf = pd.DataFrame(
        {
            "open": o[:, 0],
            "high": h.max(axis=1),
            "low": l.min(axis=1),
            "close": c[:, -1],
        },
        index=visible_at,
    )
    return htf, visible_at


def _confirmed_pivots(high: np.ndarray, low: np.ndarray, length: int):
    """Boolean arrays: bar ``i`` confirms a pivot located at bar ``i-length``.

    Replicates ``ta.pivothigh(high, length, length)`` /
    ``ta.pivotlow(low, length, length)``: the extreme must be the (leftmost)
    maximum/minimum of the ``2*length+1`` window ending at the confirmation
    bar. Ties resolve to the leftmost bar, matching Pine's left-biased pivot
    selection.
    """
    n = len(high)
    conf_high = np.zeros(n, dtype=bool)
    conf_low = np.zeros(n, dtype=bool)
    if n < 2 * length + 1 or length < 1:
        return conf_high, conf_low
    window = 2 * length + 1
    # Rolling max/min of the window ENDING at bar i; the pivot candidate is
    # the bar `length` positions left of the window end.
    try:
        roll_max = pd.Series(high).rolling(window, min_periods=window).max().to_numpy()
        roll_min = pd.Series(low).rolling(window, min_periods=window).min().to_numpy()
    except Exception:
        return conf_high, conf_low
    for i in range(2 * length, n):
        p = i - length
        hp, lp = high[p], low[p]
        if np.isnan(hp) or np.isnan(lp):
            continue
        if hp == roll_max[i]:
            # Leftmost-tie rule: no equal-or-higher bar left of p in window.
            if not np.any(high[i - window + 1:p] >= hp):
                conf_high[i] = True
        if lp == roll_min[i]:
            if not np.any(low[i - window + 1:p] <= lp):
                conf_low[i] = True
    return conf_high, conf_low


def _zones_overlap(a_top: float, a_btm: float, b_top: float, b_btm: float):
    """Common interval of two zones, or None when they do not overlap."""
    lo = a_btm if a_btm >= b_btm else b_btm
    hi = a_top if a_top <= b_top else b_top
    if lo <= hi:
        return lo, hi
    return None


class _LevelState:
    """One pivot track (internal highs/lows, swing highs/lows).

    Mirrors the Pine ``ms`` struct: ``head`` is the most recent unbroken
    level (cleared on a break), ``last_two`` keeps the two newest pivot
    prices for CHoCH classification (Pine only ever reads ``.first()`` and
    ``.get(1)`` of the ``.l`` arrays, which are never cleared).
    """

    __slots__ = ("head_price", "head_birth", "last_two")

    def __init__(self):
        self.head_price = None
        self.head_birth = -1
        self.last_two = []

    def push(self, price: float, bar: int) -> None:
        self.head_price = float(price)
        self.head_birth = int(bar)
        self.last_two.insert(0, float(price))
        del self.last_two[2:]

    def clear_head(self) -> None:
        self.head_price = None
        self.head_birth = -1


class _InlEngine:
    """Single-frame INL structure + institutional-zone state machine.

    Positional indexing (0..n-1). All decisions at bar ``i`` use data at
    bars ``<= i`` only.
    """

    def __init__(self, internal_len: int, swing_len: int, use_swing_levels: bool):
        self.internal_len = int(internal_len)
        self.swing_len = int(swing_len)
        self.use_swing_levels = bool(use_swing_levels)
        self.up = _LevelState()
        self.dn = _LevelState()
        self.sup = _LevelState()
        self.sdn = _LevelState()
        self.itrend = 0
        self.trend = 0
        self.hN = 0
        self.lN = 0
        self.hS = 0
        self.lS = 0
        self.books = {"bull": [], "bear": [], "swing_bull": [], "swing_bear": []}

    # -- zone construction -------------------------------------------------
    @staticmethod
    def _build_bull(o, h, l, c, loc: int, i: int):
        js = range(loc + 1, i + 1)
        lows = np.array([l[j] for j in js])
        ju_rel = int(np.argmin(lows))
        ju = loc + 1 + ju_rel
        btm = float(lows[ju_rel])
        top = float((h[ju] + l[ju]) / 2.0)  # hl2 — "Precise" positioning
        avg = (top + btm) / 2.0
        body_lo = o[ju] if o[ju] < c[ju] else c[ju]
        hlcc4 = (h[ju] + l[ju] + c[ju] + c[ju]) / 4.0
        if avg < body_lo and top > hlcc4:
            top = avg
            avg = (top + btm) / 2.0
        return {"top": top, "btm": btm, "avg": avg, "birth": int(i)}

    @staticmethod
    def _build_bear(o, h, l, c, loc: int, i: int):
        js = range(loc + 1, i + 1)
        highs = np.array([h[j] for j in js])
        jd_rel = int(np.argmax(highs))
        jd = loc + 1 + jd_rel
        top = float(highs[jd_rel])
        btm = float((h[jd] + l[jd]) / 2.0)  # hl2 — "Precise" positioning
        avg = (top + btm) / 2.0
        body_hi = o[jd] if o[jd] > c[jd] else c[jd]
        hlcc4 = (h[jd] + l[jd] + c[jd] + c[jd]) / 4.0
        if avg > body_hi and btm < hlcc4:
            btm = avg
            avg = (top + btm) / 2.0
        return {"top": top, "btm": btm, "avg": avg, "birth": int(i)}

    @staticmethod
    def _insert_previous(book: list, zone: dict, bullish: bool) -> None:
        """Overlap rule "Previous": keep the older zone on overlap."""
        if book:
            prev = book[0]
            if bullish and zone["btm"] < prev["top"]:
                return
            if not bullish and zone["top"] > prev["btm"]:
                return
        book.insert(0, zone)

    def _mitigate(self, close: float) -> None:
        """Absolute mitigation by the just-closed bar."""
        if np.isnan(close):
            return
        self.books["bull"] = [z for z in self.books["bull"] if close >= z["btm"]]
        self.books["swing_bull"] = [z for z in self.books["swing_bull"] if close >= z["btm"]]
        self.books["bear"] = [z for z in self.books["bear"] if close <= z["top"]]
        self.books["swing_bear"] = [z for z in self.books["swing_bear"] if close <= z["top"]]

    # -- per-bar step ------------------------------------------------------
    def step(self, i: int, o, h, l, c, ciH, ciL, csH, csL):
        """Advance the engine with closed bar ``i``.

        Returns (bull_event, bear_event, s_bull_event, s_bear_event).
        Pivot pushes run before break checks (Pine order); zone creation
        runs before same-bar mitigation (Pine order).
        """
        if i < 1:
            return False, False, False, False

        # 1) confirmed pivots (first match wins: iH, iL, sL, sH — Pine order)
        if ciH[i]:
            p = i - self.internal_len
            self.up.push(h[p], i)
            self.hN = p
        elif ciL[i]:
            p = i - self.internal_len
            self.dn.push(l[p], i)
            self.lN = p
        elif csL[i]:
            p = i - self.swing_len
            self.sdn.push(l[p], i)
            self.lS = p
        elif csH[i]:
            p = i - self.swing_len
            self.sup.push(h[p], i)
            self.hS = p

        prev_c, cur_c = c[i - 1], c[i]
        bull = bear = s_bull = s_bear = False

        # 2) structure breaks (level must predate this bar — Pine na guard)
        if (
            self.up.head_price is not None
            and self.up.head_birth < i
            and len(self.dn.last_two) > 1
            and not np.isnan(prev_c)
            and not np.isnan(cur_c)
            and prev_c <= self.up.head_price
            and cur_c > self.up.head_price
        ):
            bull = True
            self.itrend = 1
            self.up.clear_head()
        if (
            self.dn.head_price is not None
            and self.dn.head_birth < i
            and len(self.up.last_two) > 1
            and not np.isnan(prev_c)
            and not np.isnan(cur_c)
            and prev_c >= self.dn.head_price
            and cur_c < self.dn.head_price
        ):
            bear = True
            self.itrend = -1
            self.dn.clear_head()
        if (
            self.sup.head_price is not None
            and self.sup.head_birth < i
            and len(self.sdn.last_two) > 1
            and not np.isnan(prev_c)
            and not np.isnan(cur_c)
            and prev_c <= self.sup.head_price
            and cur_c > self.sup.head_price
        ):
            s_bull = True
            self.trend = 1
            self.sup.clear_head()
        if (
            self.sdn.head_price is not None
            and self.sdn.head_birth < i
            and len(self.sup.last_two) > 1
            and not np.isnan(prev_c)
            and not np.isnan(cur_c)
            and prev_c >= self.sdn.head_price
            and cur_c < self.sdn.head_price
        ):
            s_bear = True
            self.trend = -1
            self.sdn.clear_head()

        # 3) institutional zones from break events
        if bull and self.hN < i:
            self._insert_previous(
                self.books["bull"], self._build_bull(o, h, l, c, self.hN, i), True
            )
        if bear and self.lN < i:
            self._insert_previous(
                self.books["bear"], self._build_bear(o, h, l, c, self.lN, i), False
            )
        if self.use_swing_levels:
            if s_bull and self.hS < i:
                self._insert_previous(
                    self.books["swing_bull"],
                    self._build_bull(o, h, l, c, self.hS, i),
                    True,
                )
            if s_bear and self.lS < i:
                self._insert_previous(
                    self.books["swing_bear"],
                    self._build_bear(o, h, l, c, self.lS, i),
                    False,
                )

        # 4) same-bar Absolute mitigation
        self._mitigate(cur_c)
        return bull, bear, s_bull, s_bear

    def snapshot(self):
        return {
            key: [dict(z) for z in zones] for key, zones in self.books.items()
        }


def _bar_in_overlap(top: float, btm: float, bar_high: float, bar_low: float,
                    bar_close: float, touch_mode: str) -> bool:
    if touch_mode == "close":
        return btm <= bar_close <= top
    return bar_low <= top and bar_high >= btm


def _regime_label(value) -> str:
    """Normalize the injected regime snapshot or a backtest label to text."""
    if isinstance(value, dict):
        if "regime" in value:
            value = value.get("regime")
        else:
            # Defensive support for a multi-window snapshot. The framework
            # normally injects the primary (medium) snapshot directly.
            candidate = value.get("medium") or value.get("default")
            value = candidate.get("regime") if isinstance(candidate, dict) else ""
    return str(value or "").strip().lower()


def _regime_blocks_side(value, side: str) -> bool:
    """Veto only clearly opposing directional states; neutral stays neutral."""
    label = _regime_label(value)
    up = label in {
        "trending_up", "trending_up_clean", "trending_up_choppy",
        "ranging_directional_up",
    }
    down = label in {
        "trending_down", "trending_down_clean", "trending_down_choppy",
        "ranging_directional_down",
    }
    return down if side == "long" else up


def ob_touch_core(
    df: pd.DataFrame,
    internal_lookback: int = 5,
    swing_lookback: int = 50,
    inl_num: int = 7,
    use_swing_levels: bool = True,
    use_swing_filter: bool = True,
    htf_factor: int = 4,
    touch_mode: str = "wick",
    allow_short: bool = True,
    regime_direction_filter: bool = True,
    regime=None,
    htf_df: pd.DataFrame = None,
) -> pd.DataFrame:
    """Entry signals for INL institutional-zone touches with HTF overlap.

    ``htf_df`` optionally carries pre-fetched higher-timeframe candles
    (injected by the check scripts via ``--htf-timeframe``). Its forming
    (last) candle is always discarded so only completed HTF candles gate
    entries. When omitted, the HTF frame is resampled in-frame from ``df``
    (backtest path).

    When the framework supplies a directional composite/ADX regime, the
    default direction veto suppresses longs in clearly bearish labels and
    shorts in clearly bullish labels. Neutral/unknown labels are left to the
    existing swing filter and HTF zone-overlap rules.
    """
    _ = inl_num  # display cap in Pine; books here are mitigation-bounded
    if touch_mode not in _VALID_TOUCH_MODES:
        raise ValueError(
            f"touch_mode must be one of {_VALID_TOUCH_MODES}, got {touch_mode!r}"
        )
    internal_lookback = int(internal_lookback)
    swing_lookback = int(swing_lookback)
    htf_factor = max(int(htf_factor), 1)

    result = df.copy()
    n = len(result)
    result["signal"] = 0
    for col in (
        "swing_trend", "itrend",
        "bull_top", "bull_btm", "bull_avg",
        "bear_top", "bear_btm", "bear_avg",
        "htf_bull_top", "htf_bull_btm", "htf_bull_avg",
        "htf_bear_top", "htf_bear_btm", "htf_bear_avg",
    ):
        if col in ("swing_trend", "itrend"):
            result[col] = 0
        else:
            result[col] = np.nan
    if n < 2:
        return result

    o = result["open"].to_numpy(dtype=float)
    h = result["high"].to_numpy(dtype=float)
    l = result["low"].to_numpy(dtype=float)
    c = result["close"].to_numpy(dtype=float)

    if regime is None and "regime" in result.columns:
        regime_values = result["regime"].to_numpy(dtype=object)
    else:
        regime_values = np.full(n, regime, dtype=object)

    ciH, ciL = _confirmed_pivots(h, l, internal_lookback)
    csH, csL = _confirmed_pivots(h, l, swing_lookback)

    # HTF frame: same engine on pre-fetched candles when injected, else on
    # resampled candles. Snapshots are keyed by the first native bar at
    # which each HTF candle is complete (never the forming candle).
    use_htf = htf_factor > 1
    htf_snaps = []
    snap_idx = np.full(n, -1)
    if use_htf:
        htf = None
        htf_native_pos = None
        if htf_df is not None and isinstance(htf_df, pd.DataFrame) and len(htf_df) > 1:
            try:
                htf = htf_df[["open", "high", "low", "close"]].iloc[:-1].dropna(subset=["close"])
            except Exception:
                htf = None
        if htf is not None and len(htf) > 1 and isinstance(htf.index, pd.DatetimeIndex) \
                and isinstance(result.index, pd.DatetimeIndex):
            diffs = htf.index.to_series().diff().dropna()
            htf_td = diffs.mode().iloc[0] if len(diffs) > 0 else None
            if htf_td is not None and htf_td > pd.Timedelta(0):
                htf_native_pos = (htf.index + htf_td).searchsorted(result.index, side="right") - 1
        if htf is None or htf_native_pos is None:
            htf, visible_at = _resample_htf(result, htf_factor)
            if len(htf) > 1:
                try:
                    htf_native_pos = visible_at.searchsorted(result.index, side="right") - 1
                except Exception:
                    htf_native_pos = None
            else:
                htf = None
        if htf is not None and htf_native_pos is not None and len(htf) > 1:
            ho = htf["open"].to_numpy(dtype=float)
            hh = htf["high"].to_numpy(dtype=float)
            hl = htf["low"].to_numpy(dtype=float)
            hc = htf["close"].to_numpy(dtype=float)
            hciH, hciL = _confirmed_pivots(hh, hl, internal_lookback)
            hcsH, hcsL = _confirmed_pivots(hh, hl, swing_lookback)
            heng = _InlEngine(internal_lookback, swing_lookback, use_swing_levels)
            for k in range(len(htf)):
                heng.step(k, ho, hh, hl, hc, hciH, hciL, hcsH, hcsL)
                htf_snaps.append(heng.snapshot())
            try:
                snap_idx = np.asarray(htf_native_pos, dtype=int)
            except Exception:
                snap_idx = np.full(n, -1)

    eng = _InlEngine(internal_lookback, swing_lookback, use_swing_levels)
    signals = np.zeros(n, dtype=int)
    swing_trend = np.zeros(n, dtype=int)
    itrend_arr = np.zeros(n, dtype=int)
    _loc = {col: result.columns.get_loc(col) for col in (
        "bull_top", "bull_btm", "bull_avg",
        "bear_top", "bear_btm", "bear_avg",
        "htf_bull_top", "htf_bull_btm", "htf_bull_avg",
        "htf_bear_top", "htf_bear_btm", "htf_bear_avg",
    )}

    for i in range(n):
        eng.step(i, o, h, l, c, ciH, ciL, csH, csL)
        swing_trend[i] = eng.trend
        itrend_arr[i] = eng.itrend
        if i < 1:
            continue

        if use_htf:
            k = snap_idx[i]
            if k < 0 or k >= len(htf_snaps):
                continue
            h_books = htf_snaps[k]
            h_bull = h_books["bull"] + h_books["swing_bull"]
            h_bear = h_books["bear"] + h_books["swing_bear"]
        else:
            h_bull = h_bear = None

        l_bull = eng.books["bull"] + eng.books["swing_bull"]
        l_bear = eng.books["bear"] + eng.books["swing_bear"]
        bar_regime = regime_values[i]
        regime_long_ok = (
            not regime_direction_filter
            or not _regime_blocks_side(bar_regime, "long")
        )
        regime_short_ok = (
            not regime_direction_filter
            or not _regime_blocks_side(bar_regime, "short")
        )

        fired_long = False
        if regime_long_ok and (not use_swing_filter or eng.trend == 1):
            for z in l_bull:
                peers = [z] if not use_htf else h_bull
                for hz in peers:
                    ov = _zones_overlap(z["top"], z["btm"], hz["top"], hz["btm"])
                    if ov is None:
                        continue
                    ov_lo, ov_hi = ov
                    now = _bar_in_overlap(ov_hi, ov_lo, h[i], l[i], c[i], touch_mode)
                    prev = _bar_in_overlap(ov_hi, ov_lo, h[i - 1], l[i - 1], c[i - 1], touch_mode)
                    if now and not prev:
                        fired_long = True
                        break
                if fired_long:
                    break
        fired_short = False
        if allow_short and regime_short_ok and (not use_swing_filter or eng.trend == -1):
            for z in l_bear:
                peers = [z] if not use_htf else h_bear
                for hz in peers:
                    ov = _zones_overlap(z["top"], z["btm"], hz["top"], hz["btm"])
                    if ov is None:
                        continue
                    ov_lo, ov_hi = ov
                    now = _bar_in_overlap(ov_hi, ov_lo, h[i], l[i], c[i], touch_mode)
                    prev = _bar_in_overlap(ov_hi, ov_lo, h[i - 1], l[i - 1], c[i - 1], touch_mode)
                    if now and not prev:
                        fired_short = True
                        break
                if fired_short:
                    break

        if fired_long:
            signals[i] = 1
        elif fired_short:
            signals[i] = -1

        # Diagnostics: newest active zone per side (NaN when book is empty).
        if l_bull:
            result.iat[i, _loc["bull_top"]] = l_bull[0]["top"]
            result.iat[i, _loc["bull_btm"]] = l_bull[0]["btm"]
            result.iat[i, _loc["bull_avg"]] = l_bull[0]["avg"]
        if l_bear:
            result.iat[i, _loc["bear_top"]] = l_bear[0]["top"]
            result.iat[i, _loc["bear_btm"]] = l_bear[0]["btm"]
            result.iat[i, _loc["bear_avg"]] = l_bear[0]["avg"]
        if use_htf:
            k = snap_idx[i]
            if 0 <= k < len(htf_snaps):
                hb = htf_snaps[k]["bull"] + htf_snaps[k]["swing_bull"]
                hr = htf_snaps[k]["bear"] + htf_snaps[k]["swing_bear"]
                if hb:
                    result.iat[i, _loc["htf_bull_top"]] = hb[0]["top"]
                    result.iat[i, _loc["htf_bull_btm"]] = hb[0]["btm"]
                    result.iat[i, _loc["htf_bull_avg"]] = hb[0]["avg"]
                if hr:
                    result.iat[i, _loc["htf_bear_top"]] = hr[0]["top"]
                    result.iat[i, _loc["htf_bear_btm"]] = hr[0]["btm"]
                    result.iat[i, _loc["htf_bear_avg"]] = hr[0]["avg"]

    result["signal"] = signals
    result["swing_trend"] = swing_trend
    result["itrend"] = itrend_arr
    return result
