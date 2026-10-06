from __future__ import annotations

import json
import pathlib
import sys
from argparse import Namespace
import importlib.util
from types import SimpleNamespace

import pandas as pd

_ROOT = pathlib.Path(__file__).resolve().parents[1]
_TOOLS = _ROOT / "shared_tools"
for _path in (_ROOT, _TOOLS):
    if str(_path) not in sys.path:
        sys.path.insert(0, str(_path))

from shared_tools.conftest import load_module
from shared_tools.strategy_composition import parse_strategy_refs_arg

_CHECK = load_module(
    "_check_blofin_close_context_test",
    pathlib.Path(__file__).with_name("check_blofin.py"),
)
_CLOSE_DIR = _ROOT / "shared_strategies" / "close"
if str(_CLOSE_DIR) not in sys.path:
    sys.path.insert(0, str(_CLOSE_DIR))


def _load_close(name):
    path = _CLOSE_DIR / f"{name}.py"
    spec = importlib.util.spec_from_file_location(f"_{name}_blofin_context_test", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _frame(times, closes, volumes=None):
    volumes = volumes or [1.0] * len(closes)
    return pd.DataFrame(
        {
            "open": closes,
            "high": closes,
            "low": closes,
            "close": closes,
            "volume": volumes,
        },
        index=pd.DatetimeIndex(times, tz="UTC"),
    )


def test_position_context_parses_bars_open_time_and_applied_regime():
    args = Namespace(
        position_side="long",
        position_avg_cost=100.0,
        position_qty=2.0,
        position_initial_qty=3.0,
        position_entry_atr=4.0,
        position_regime="trending_up",
        position_regime_applied="ranging",
        position_regime_pending_label="ranging",
        position_regime_pending_count=1,
        position_bars_held=17,
        position_opened_at_ms=1_791_021_600_000,
    )
    ctx = _CHECK._position_ctx_from_args(args)
    assert ctx["bars_held"] == 17
    assert ctx["opened_at_ms"] == args.position_opened_at_ms
    assert ctx["regime"] == "trending_up"
    assert ctx["regime_applied_label"] == "ranging"
    assert ctx["regime_pending_label"] == "ranging"
    assert ctx["regime_pending_count"] == 1


def test_zscore_uses_only_completed_candles_and_configured_lookback():
    df = _frame(
        ["2026-10-03 10:00", "2026-10-03 10:15", "2026-10-03 10:30", "2026-10-03 10:45"],
        [100.0, 110.0, 120.0, 900.0],
    )
    now = pd.Timestamp("2026-10-03 10:45", tz="UTC")
    assert _CHECK._closed_candle_zscore(df, "15m", 2, now) == 1.0


def test_zscore_missing_warmup_and_zero_variance_are_not_fabricated():
    now = pd.Timestamp("2026-10-03 10:45", tz="UTC")
    short = _frame(["2026-10-03 10:15", "2026-10-03 10:30"], [100.0, 101.0])
    flat = _frame(["2026-10-03 10:00", "2026-10-03 10:15", "2026-10-03 10:30"], [100.0] * 3)
    assert _CHECK._closed_candle_zscore(short, "15m", 3, now) is None
    assert _CHECK._closed_candle_zscore(flat, "15m", 2, now) is None


def test_position_anchored_avwap_excludes_entry_bar_and_uses_closed_bars():
    df = _frame(
        ["2026-10-03 10:00", "2026-10-03 10:15", "2026-10-03 10:30", "2026-10-03 10:45"],
        [1_000.0, 100.0, 200.0, 9_000.0],
        [100.0, 1.0, 3.0, 100.0],
    )
    opened_at_ms = int(pd.Timestamp("2026-10-03 10:07", tz="UTC").timestamp() * 1000)
    now = pd.Timestamp("2026-10-03 10:45", tz="UTC")
    got = _CHECK._position_anchored_avwap(df, "15m", opened_at_ms, now)
    assert got == 175.0


def test_position_anchored_avwap_refuses_history_that_misses_the_anchor():
    df = _frame(["2026-10-03 10:30", "2026-10-03 10:45"], [100.0, 110.0])
    opened_at_ms = int(pd.Timestamp("2026-10-03 10:07", tz="UTC").timestamp() * 1000)
    now = pd.Timestamp("2026-10-03 11:00", tz="UTC")
    assert _CHECK._position_anchored_avwap(df, "15m", opened_at_ms, now) is None


def test_monthly_time_stop_counts_completed_calendar_bars():
    df = _frame(
        ["2026-01-01", "2026-02-01", "2026-03-01"],
        [100.0, 101.0, 102.0],
    )
    opened_at_ms = int(pd.Timestamp("2026-02-15", tz="UTC").timestamp() * 1000)
    now = pd.Timestamp("2026-03-01", tz="UTC")
    assert _CHECK._completed_bars_held_from_candles(df, "1M", opened_at_ms, now) == 1


def test_zscore_target_closes_long_and_short_on_opposite_signed_targets():
    evaluate = _load_close("zscore_target").evaluate
    params = {"lookback": 20, "z_target": 1.5}
    long_pos = {"side": "long", "current_quantity": 1.0}
    short_pos = {"side": "short", "current_quantity": 1.0}
    assert evaluate(long_pos, {"zscore": 1.5}, params)["close_fraction"] == 1.0
    assert evaluate(short_pos, {"zscore": -1.5}, params)["close_fraction"] == 1.0
    assert evaluate(long_pos, {"zscore": -1.5}, params)["close_fraction"] == 0.0


def test_time_stop_consumes_the_scheduler_completed_bar_count():
    evaluate = _load_close("time_stop").evaluate
    pos = {"bars_held": 12}
    assert evaluate(pos, {}, {"max_bars": 12})["close_fraction"] == 1.0
    assert evaluate({}, {}, {"max_bars": 12})["reason"] == "noop:missing_bars_held"


def test_dynamic_close_prices_tp_from_the_confirmed_applied_label():
    evaluate = _load_close("tiered_tp_atr_live_regime_dynamic").evaluate
    params = {
        "atr_source": "live",
        "trend_regime": {
            "trending_up": {
                "stop_loss_atr": 2.0,
                "tp_tiers": [{"atr_multiple": 4.0, "close_fraction": 1.0}],
            },
            "trending_down": {
                "stop_loss_atr": 2.0,
                "tp_tiers": [{"atr_multiple": 2.0, "close_fraction": 1.0}],
            },
            "ranging": {
                "stop_loss_atr": 1.5,
                "tp_tiers": [{"atr_multiple": 1.0, "close_fraction": 1.0}],
            },
        },
    }
    position = {
        "side": "long",
        "avg_cost": 100.0,
        "current_quantity": 1.0,
        "initial_quantity": 1.0,
        "entry_atr": 5.0,
        "regime": "trending_up",
        "regime_applied_label": "ranging",
    }
    result = evaluate(position, {"mark_price": 106.0, "atr": 5.0, "regime": "trending_down"}, params)
    assert result["close_fraction"] == 1.0
    assert "regime=applied:ranging" in result["reason"]


def test_dynamic_close_previews_new_ladder_only_on_confirming_cycle():
    evaluate = _load_close("tiered_tp_atr_live_regime_dynamic").evaluate
    params = {
        "regime_confirm_cycles": 2,
        "trend_regime": {
            "trending_up": {
                "stop_loss_atr": 2.0,
                "tp_tiers": [{"atr_multiple": 4.0, "close_fraction": 1.0}],
            },
            "trending_down": {
                "stop_loss_atr": 2.0,
                "tp_tiers": [{"atr_multiple": 2.0, "close_fraction": 1.0}],
            },
            "ranging": {
                "stop_loss_atr": 1.5,
                "tp_tiers": [{"atr_multiple": 1.0, "close_fraction": 1.0}],
            },
        },
    }
    position = {
        "side": "long", "avg_cost": 100.0, "current_quantity": 1.0,
        "initial_quantity": 1.0, "entry_atr": 5.0,
        "regime": "trending_up", "regime_applied_label": "trending_up",
        "regime_pending_label": "ranging", "regime_pending_count": 0,
    }
    market = {"mark_price": 106.0, "atr": 5.0, "regime": "ranging"}
    first_cycle = evaluate(position, market, params)
    assert first_cycle["close_fraction"] == 0.0

    position["regime_pending_count"] = 1
    confirming_cycle = evaluate(position, market, params)
    assert confirming_cycle["close_fraction"] == 1.0
    assert "regime=applied:ranging" in confirming_cycle["reason"]


def test_blofin_check_strategy_refs_carry_close_reason_and_zscore(monkeypatch, capsys):
    now = pd.Timestamp.now(tz="UTC").floor("h")
    index = pd.date_range(end=now, periods=40, freq="1h")
    closes = [100.0] * len(index)
    closes[-2] = 110.0  # Last completed candle; z-score = +1 over the last 2 closes.
    closes[-1] = 999.0  # Forming candle must not enter the z-score.
    highs = [value + 1.0 for value in closes]
    lows = [value - 1.0 for value in closes]
    candles = [
        [int(ts.timestamp() * 1000), value, hi, lo, value, 10.0]
        for ts, value, hi, lo in zip(index, closes, highs, lows)
    ]

    class FakeAdapter:
        def __init__(self):
            pass

        def get_perp_ohlcv(self, symbol, interval="1h", limit=200):
            return candles

        def get_perp_price(self, symbol):
            return 110.0

    monkeypatch.setattr(
        _CHECK,
        "_blofin_adapter",
        lambda: SimpleNamespace(BloFinExchangeAdapter=FakeAdapter),
    )
    opened_at_ms = int((now - pd.Timedelta(hours=5)).timestamp() * 1000)
    refs = parse_strategy_refs_arg(json.dumps({
        "open": {"name": "hold"},
        "closes": [{
            "name": "zscore_target",
            "params": {"lookback": 2, "z_target": 1.0},
        }],
    }))
    _CHECK.run_signal_check(
        "hold",
        "BTC",
        "1h",
        "paper",
        open_strategy=refs["open_name"],
        close_strategies=refs["close_csv"],
        position_side="long",
        position_ctx={
            "side": "long",
            "avg_cost": 100.0,
            "current_quantity": 1.0,
            "initial_quantity": 1.0,
            "entry_atr": 2.0,
            "opened_at_ms": opened_at_ms,
            "bars_held": 5,
        },
        close_params_by_name=refs["close_params_by_name"],
    )
    payload = json.loads(capsys.readouterr().out.strip().splitlines()[-1])
    assert payload["signal"] == -1
    assert payload["close_fraction"] == 1.0
    assert payload["close_evaluator"] == "zscore_target"
    assert payload["close_reason"] == "zscore_target:1"
    assert payload["indicators"]["zscore"] == 1.0
