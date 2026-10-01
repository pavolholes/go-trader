
import numpy as np
import pandas as pd

from shared_strategies.open.conftest import load_module, make_ohlcv

_OB_TOUCH = load_module("_ob_touch_test", __file__.replace("test_ob_touch.py", "ob_touch.py"))
ob_touch_core = _OB_TOUCH.ob_touch_core
_zones_overlap = _OB_TOUCH._zones_overlap
_bar_in_overlap = _OB_TOUCH._bar_in_overlap
_confirmed_pivots = _OB_TOUCH._confirmed_pivots


def _scenario():
    """V-shape with an internal BOS at bar 9 and a pullback into the zone.

    Hand-verified with internal_lookback=2: bar 9 closes 103 above the
    confirmed internal high 102.3 (pivot at bar 2), creating a bullish zone
    top=98.75 / btm=98.0 / avg=98.375. Bar 10 wicks into it (first touch),
    bar 11 stays inside (no repeat), bar 12 closes 97 below the bottom
    (Absolute mitigation), bar 13 wicks the old bounds (dead zone, no fire).
    """
    closes = [100, 101, 102, 100, 99, 100, 99, 99, 101, 103, 100, 99, 97, 98.2]
    highs = [100.3, 101.3, 102.3, 100.3, 99.3, 100.3, 99.3, 99.5, 101.3, 103.3,
             100.5, 99.5, 98.0, 98.7]
    lows = [99.7, 100.7, 101.7, 99.3, 98.3, 99.3, 98.5, 98.0, 100.5, 102.5,
            98.2, 98.3, 96.5, 98.1]
    return make_ohlcv(closes, opens=closes, highs=highs, lows=lows)


def _mirror(df: pd.DataFrame) -> pd.DataFrame:
    """Vertical mirror: bullish setups become bearish with flipped signals."""
    out = df.copy()
    out["high"] = 200.0 - df["low"]
    out["low"] = 200.0 - df["high"]
    out["open"] = 200.0 - df["open"]
    out["close"] = 200.0 - df["close"]
    return out


def _run(df, **params):
    base = dict(internal_lookback=2, swing_lookback=6, inl_num=5,
                use_swing_levels=True, use_swing_filter=False,
                htf_factor=1, touch_mode="wick", allow_short=True)
    base.update(params)
    return ob_touch_core(df, **base)


class TestEntry:
    def test_first_touch_fires_once(self):
        out = _run(_scenario())
        assert out["signal"].iloc[10] == 1
        assert (out["signal"] != 0).sum() == 1

    def test_zone_levels_match_hand_computation(self):
        out = _run(_scenario())
        assert out["bull_top"].iloc[10] == 98.75
        assert out["bull_btm"].iloc[10] == 98.0
        assert out["bull_avg"].iloc[10] == 98.375

    def test_mitigated_zone_never_fires_again(self):
        out = _run(_scenario())
        assert out["signal"].iloc[12] == 0
        assert out["signal"].iloc[13] == 0
        assert np.isnan(out["bull_top"].iloc[13])

    def test_mirror_scenario_fires_short(self):
        out = _run(_mirror(_scenario()))
        assert out["signal"].iloc[10] == -1
        assert (out["signal"] != 0).sum() == 1

    def test_reentry_after_exit_fires_again(self):
        # Pine parity: obtouch is a fresh crossunder on every re-entry.
        # Touch bar 10, leave bar 11 (low back above the top), re-enter
        # bar 12 (fires again), stay inside bar 13 (no repeat).
        closes = [100, 101, 102, 100, 99, 100, 99, 99, 101, 103,
                  100, 100.5, 100, 100]
        highs = [100.3, 101.3, 102.3, 100.3, 99.3, 100.3, 99.3, 99.5,
                 101.3, 103.3, 100.5, 101, 101, 100.5]
        lows = [99.7, 100.7, 101.7, 99.3, 98.3, 99.3, 98.5, 98.0,
                100.5, 102.5, 98.2, 99.0, 98.3, 98.4]
        df = make_ohlcv(closes, opens=closes, highs=highs, lows=lows)
        out = _run(df)
        assert out["signal"].iloc[10] == 1
        assert out["signal"].iloc[11] == 0
        assert out["signal"].iloc[12] == 1
        assert out["signal"].iloc[13] == 0
        assert (out["signal"] != 0).sum() == 2

    def test_close_touch_mode_ignores_wick(self):
        out = _run(_scenario(), touch_mode="close")
        assert (out["signal"] != 0).sum() == 0


class TestFilters:
    def test_swing_filter_blocks_without_swing_trend(self):
        # Swing lookback 6 never confirms a pivot on 14 bars, so trend == 0.
        out = _run(_scenario(), use_swing_filter=True)
        assert (out["signal"] != 0).sum() == 0
        assert (out["swing_trend"] == 0).all()

    def test_allow_short_false_suppresses_shorts_only(self):
        out = _run(_mirror(_scenario()), allow_short=False)
        assert (out["signal"] != 0).sum() == 0
        out_long = _run(_scenario(), allow_short=False)
        assert out_long["signal"].iloc[10] == 1

    def test_invalid_touch_mode_rejected(self):
        try:
            _run(_scenario(), touch_mode="mid")
        except ValueError:
            pass
        else:
            raise AssertionError("expected ValueError for touch_mode='mid'")


class TestHtfGate:
    def test_htf_without_history_stays_flat(self):
        out = _run(_scenario(), htf_factor=12)
        assert (out["signal"] != 0).sum() == 0

    def test_single_tf_mode_needs_no_htf(self):
        out = _run(_scenario(), htf_factor=1)
        assert out["signal"].iloc[10] == 1

    def _htf_frame(self, extra_forming_bar=True):
        df = _scenario()
        df.index = pd.date_range("2026-01-01", periods=len(df), freq="1h")
        if extra_forming_bar:
            wild = pd.DataFrame(
                {"open": [500.0], "high": [600.0], "low": [400.0],
                 "close": [500.0], "volume": [1.0]},
                index=pd.date_range("2026-01-01", periods=15, freq="1h")[14:],
            )
            df = pd.concat([df, wild])
        return df

    def _ltf_frame(self):
        # LTF episode runs after the HTF zone is complete: HTF bar 9 closes
        # 10:00, so the LTF touch (bar 10) lands at 12:30 against HTF state
        # of bar 11 (zone still active — mitigated only at HTF bar 12).
        df = _scenario()
        df.index = pd.date_range("2026-01-01 10:00", periods=len(df), freq="15min")
        return df

    def test_injected_htf_gates_entries(self):
        out = ob_touch_core(
            self._ltf_frame(), internal_lookback=2, swing_lookback=6,
            inl_num=5, use_swing_filter=False, htf_factor=4,
            htf_df=self._htf_frame(),
        )
        assert out["signal"].iloc[10] == 1
        assert (out["signal"] != 0).sum() == 1

    def test_injected_forming_htf_candle_ignored(self):
        kw = dict(internal_lookback=2, swing_lookback=6, inl_num=5,
                  use_swing_filter=False, htf_factor=4)
        ltf = self._ltf_frame()
        with_forming = ob_touch_core(ltf, htf_df=self._htf_frame(True), **kw)
        without = ob_touch_core(ltf, htf_df=self._htf_frame(False), **kw)
        pd.testing.assert_series_equal(with_forming["signal"], without["signal"])

    def test_injected_htf_without_overlap_stays_flat(self):
        ltf = self._ltf_frame()
        for col in ("open", "high", "low", "close"):
            ltf[col] = ltf[col] + 50.0
        out = ob_touch_core(
            ltf, internal_lookback=2, swing_lookback=6, inl_num=5,
            use_swing_filter=False, htf_factor=4,
            htf_df=self._htf_frame(),
        )
        assert (out["signal"] != 0).sum() == 0


class TestHelpers:
    def test_zones_overlap(self):
        assert _zones_overlap(10.0, 8.0, 9.0, 7.0) == (8.0, 9.0)
        assert _zones_overlap(10.0, 8.0, 7.0, 6.0) is None
        assert _zones_overlap(10.0, 8.0, 8.0, 6.0) == (8.0, 8.0)

    def test_bar_in_overlap_modes(self):
        assert _bar_in_overlap(10.0, 8.0, 9.0, 7.0, 8.5, "wick") is True
        assert _bar_in_overlap(10.0, 8.0, 7.5, 7.0, 7.2, "wick") is False
        assert _bar_in_overlap(10.0, 8.0, 11.0, 7.0, 8.5, "close") is True
        assert _bar_in_overlap(10.0, 8.0, 11.0, 7.0, 10.5, "close") is False


class TestDepth:
    def test_pivots_need_full_window(self):
        high = np.linspace(100.0, 110.0, 10)
        low = np.linspace(99.0, 109.0, 10)
        ch, cl = _confirmed_pivots(high, low, 5)
        assert not ch.any() and not cl.any()

    def test_pivots_confirm_with_enough_history(self):
        high = np.array([10.0, 11, 12, 20, 12, 11, 10, 9, 8, 7, 6.0])
        low = np.linspace(5.0, 4.0, 11)
        ch, _ = _confirmed_pivots(high, low, 2)
        assert ch[5] and ch.sum() == 1


class TestRobustness:
    def test_short_data_returns_zeros(self):
        df = make_ohlcv([100.0] * 5)
        out = _run(df)
        assert (out["signal"] == 0).all()
        assert "bull_top" in out.columns and "htf_bear_avg" in out.columns

    def test_signal_at_k_independent_of_bars_after_k(self):
        rng = np.random.RandomState(7)
        n = 300
        closes = 100 + np.cumsum(rng.randn(n) * 0.8)
        df = make_ohlcv(closes, freq="5min")
        full = ob_touch_core(df, internal_lookback=3, swing_lookback=9,
                             inl_num=5, htf_factor=4)
        signal_bars = list(np.where(full["signal"].values != 0)[0])
        for k in signal_bars:
            partial = ob_touch_core(df.iloc[: k + 1], internal_lookback=3,
                                    swing_lookback=9, inl_num=5, htf_factor=4)
            assert partial["signal"].iloc[k] == full["signal"].iloc[k], (
                f"signal at bar {k} uses future bars"
            )

    def test_registry_defaults_per_platform(self):
        import os
        import importlib.util
        here = os.path.dirname(os.path.abspath(__file__))
        spec = importlib.util.spec_from_file_location(
            "_ob_touch_registry_test", os.path.join(here, "registry.py"))
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)
        spot = mod.build_registry("spot")["ob_touch"]
        fut = mod.build_registry("futures")["ob_touch"]
        assert spot["default_params"]["allow_short"] is False
        assert fut["default_params"]["allow_short"] is True
        assert spot["default_params"]["htf_factor"] == 4
        assert spot["default_params"]["inl_num"] == 7
