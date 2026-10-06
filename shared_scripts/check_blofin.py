#!/usr/bin/env python3
"""
BloFin perps strategy check script.
Fetches OHLCV from BloFin via BloFinExchangeAdapter, runs strategy, outputs JSON to stdout, exits.

Signal check mode (paper or live):
    check_blofin.py <strategy> <symbol> <timeframe> [--mode=paper|live] [--htf-filter] [--inst-type=swap]

Execution mode (live only, called by Go as phase 2):
    check_blofin.py --execute --symbol=BTC --side=buy|sell --size=0.01 [--mode=live]
"""

import sys
import os
import json
import math
import traceback
import uuid
from datetime import datetime, timezone

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'platforms', 'blofin'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'shared_tools'))


def _blofin_adapter():
    """Resolve platforms/blofin/adapter.py.

    Both BloFin and Hyperliquid ship a top-level ``adapter`` module, so under
    a shared pytest process the first import wins the ``sys.modules`` cache
    and can shadow the other platform. A plain import is tried first; when
    the cached module is not ours, the file is loaded by path under a
    distinct module name.
    """
    import sys as _sys
    import os as _os
    mod = _sys.modules.get("blofin_platform_adapter")
    if mod is not None:
        return mod
    try:
        # NOTE: plain __import__ (not importlib.import_module) so test
        # doubles patching builtins.__import__ keep working.
        cand = __import__("adapter", fromlist=["BloFinExchangeAdapter"])
    except ImportError:
        cand = None
    cand_file = getattr(cand, "__file__", None)
    if isinstance(cand_file, str):
        if cand_file.replace(_os.sep, "/").endswith("platforms/blofin/adapter.py"):
            _sys.modules["blofin_platform_adapter"] = cand
            return cand
    elif cand is not None and hasattr(cand, "BloFinExchangeAdapter"):
        # Test double (no real __file__): use it but never cache it, so it
        # cannot leak into other tests sharing this process.
        return cand
    import importlib.util as _ilu
    path = _os.path.join(
        _os.path.dirname(_os.path.abspath(__file__)),
        "..", "platforms", "blofin", "adapter.py",
    )
    spec = _ilu.spec_from_file_location("blofin_platform_adapter", path)
    mod = _ilu.module_from_spec(spec)
    _sys.modules["blofin_platform_adapter"] = mod
    spec.loader.exec_module(mod)
    return mod

from atr import ensure_atr_indicator, latest_atr
from regime import latest_regime, parse_regime_windows_spec_json, prepare_check_regime
from ob_touch_data import closed_ltf_frame

# Minimum LTF history measured against a deep reference: NQ needs about 2k
# 5m bars and 1.2k 15m bars before the swing-50 filter produces stable state.
# This is independent of the separately fetched HTF history.
_OB_TOUCH_MIN_LTF_BARS = {"5m": 2000, "15m": 1200}


def _ob_touch_ltf_limit(timeframe: str, requested: int) -> int:
    floor = _OB_TOUCH_MIN_LTF_BARS.get(str(timeframe).lower(), 600)
    try:
        return max(int(requested or 0), floor)
    except (TypeError, ValueError):
        return floor

def _detect_inst_type(argv) -> str:
    """Pick 'swap' vs 'spot' from raw argv.

    Accepts both ``--inst-type=swap`` and ``--inst-type swap`` because demo
    configs use the space-separated form.
    """
    for idx, arg in enumerate(argv):
        if arg.startswith("--inst-type="):
            return arg.split("=", 1)[1]
        if arg == "--inst-type" and idx + 1 < len(argv):
            return argv[idx + 1]
    return "swap"


_inst_type = _detect_inst_type(sys.argv)

if _inst_type == "spot":
    sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'shared_strategies', 'open', 'spot'))
else:
    sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'shared_strategies', 'open', 'futures'))


def _make_dataframe(candles):
    import pandas as pd
    df = pd.DataFrame(candles, columns=["timestamp", "open", "high", "low", "close", "volume"])
    df["datetime"] = pd.to_datetime(df["timestamp"], unit="ms", utc=True)
    df = df.set_index("datetime")
    df.sort_index(inplace=True)
    return df


def _position_ctx_from_args(args):
    ctx = {}
    side = (args.position_side or "").lower()
    if side:
        ctx["side"] = side
    for attr, key in (
        ("position_avg_cost", "avg_cost"),
        ("position_qty", "current_quantity"),
        ("position_initial_qty", "initial_quantity"),
        ("position_entry_atr", "entry_atr"),
    ):
        value = getattr(args, attr, None)
        if value is not None:
            ctx[key] = value
    regime = (getattr(args, "position_regime", "") or "").strip()
    if regime:
        ctx["regime"] = regime
    applied = (getattr(args, "position_regime_applied", "") or "").strip()
    if applied:
        ctx["regime_applied_label"] = applied
    pending_label = (getattr(args, "position_regime_pending_label", "") or "").strip()
    if pending_label:
        ctx["regime_pending_label"] = pending_label
        ctx["regime_pending_count"] = max(
            0, int(getattr(args, "position_regime_pending_count", 0) or 0)
        )
    bars_held = getattr(args, "position_bars_held", None)
    if bars_held is not None:
        ctx["bars_held"] = max(0, int(bars_held))
    opened_at_ms = getattr(args, "position_opened_at_ms", None)
    if opened_at_ms is not None:
        ctx["opened_at_ms"] = int(opened_at_ms)
    return ctx


def _timeframe_duration_ms(timeframe):
    value = str(timeframe or "").strip()
    if len(value) < 2:
        return 0
    try:
        count = int(value[:-1])
    except (TypeError, ValueError):
        return 0
    if count <= 0:
        return 0
    raw_unit = value[-1]
    unit = raw_unit.lower()
    scale = {"m": 60_000, "h": 3_600_000, "d": 86_400_000, "w": 604_800_000}.get(unit)
    if raw_unit == "M":
        scale = 30 * 86_400_000
    if scale is None:
        return 0
    return count * scale


def _closed_candles(df, timeframe, now=None):
    import pandas as pd

    duration_ms = _timeframe_duration_ms(timeframe)
    if duration_ms <= 0 or df is None or df.empty:
        return df.iloc[0:0] if df is not None else df
    now = pd.Timestamp.now(tz="UTC") if now is None else pd.Timestamp(now)
    if now.tzinfo is None:
        now = now.tz_localize("UTC")
    else:
        now = now.tz_convert("UTC")
    if str(timeframe or "").strip().endswith("M"):
        try:
            months = int(str(timeframe).strip()[:-1])
        except (TypeError, ValueError):
            return df.iloc[0:0]
        if months <= 0:
            return df.iloc[0:0]
        closes_at = df.index + pd.DateOffset(months=months)
    else:
        closes_at = df.index + pd.to_timedelta(duration_ms, unit="ms")
    return df.loc[closes_at <= now]


def _completed_bars_held_from_candles(df, timeframe, opened_at_ms, now=None):
    import pandas as pd

    try:
        opened_at_ms = int(opened_at_ms)
    except (TypeError, ValueError):
        return None
    if opened_at_ms <= 0:
        return None
    candles = _closed_candles(df, timeframe, now)
    if candles is None or candles.empty:
        return None
    opened_at = pd.Timestamp(opened_at_ms, unit="ms", tz="UTC")
    if candles.index[0] > opened_at:
        return None
    duration_ms = _timeframe_duration_ms(timeframe)
    if str(timeframe or "").strip().endswith("M"):
        months = int(str(timeframe).strip()[:-1])
        closes_at = candles.index + pd.DateOffset(months=months)
    else:
        closes_at = candles.index + pd.to_timedelta(duration_ms, unit="ms")
    return int((closes_at > opened_at).sum())


def _configured_zscore_params(close_names, close_params_by_name):
    params_by_name = close_params_by_name or {}
    for name in close_names or []:
        if str(name or "").strip().lower() != "zscore_target":
            continue
        params = params_by_name.get(name, params_by_name.get("zscore_target", {})) or {}
        try:
            lookback = int(params.get("lookback", 0) or 0)
            target = float(params.get("z_target", 0.0) or 0.0)
        except (TypeError, ValueError):
            return 0, 0.0
        return lookback, target
    return 0, 0.0


def _closed_candle_zscore(df, timeframe, lookback, now=None):
    if lookback < 2:
        return None
    candles = _closed_candles(df, timeframe, now)
    if candles is None or len(candles) < lookback or "close" not in candles:
        return None
    import pandas as pd

    closes = pd.to_numeric(candles["close"], errors="coerce").dropna().iloc[-lookback:]
    if len(closes) != lookback:
        return None
    values = closes.to_numpy(dtype=float)
    mean = float(values.mean())
    std = float(values.std(ddof=0))
    if not math.isfinite(mean) or not math.isfinite(std) or std <= 0:
        return None
    zscore = (float(values[-1]) - mean) / std
    return zscore if math.isfinite(zscore) else None


def _position_anchored_avwap(df, timeframe, opened_at_ms, now=None):
    import pandas as pd

    try:
        opened_at_ms = int(opened_at_ms)
    except (TypeError, ValueError):
        return None
    if opened_at_ms <= 0 or _timeframe_duration_ms(timeframe) <= 0:
        return None
    candles = _closed_candles(df, timeframe, now)
    if candles is None or candles.empty:
        return None
    opened_at = pd.Timestamp(opened_at_ms, unit="ms", tz="UTC")
    if candles.index[0] > opened_at:
        return None
    # A bar already in progress at entry has no within-bar volume split, so
    # anchor at the first candle open at/after the position-open timestamp.
    anchor_idx = candles.index.searchsorted(opened_at, side="left")
    if anchor_idx >= len(candles):
        return None
    candles = candles.iloc[anchor_idx:]
    if candles.empty:
        return None
    volume = pd.to_numeric(candles["volume"], errors="coerce").fillna(0.0)
    typical = (
        pd.to_numeric(candles["high"], errors="coerce")
        + pd.to_numeric(candles["low"], errors="coerce")
        + pd.to_numeric(candles["close"], errors="coerce")
    ) / 3.0
    valid = volume > 0
    total_volume = float(volume.loc[valid].sum())
    if total_volume <= 0:
        return None
    avwap = float((typical.loc[valid] * volume.loc[valid]).sum() / total_volume)
    return avwap if math.isfinite(avwap) and avwap > 0 else None


def run_signal_check(strategy_name, symbol, timeframe, mode, htf_filter_enabled=False,
                     inst_type="swap", strategy_params_override=None,
                     open_strategy=None, close_strategies=None,
                     position_side="", position_ctx=None,
                     regime_enabled=False, regime_windows_spec=None, ohlcv_limit=200, regime_atr_window="",
                     regime_payload_json=None,
                     atr_method="simple",
                     close_params_by_name=None,
                     htf_timeframe="", htf_limit=300):
    """Run strategy signal check using BloFin OHLCV data."""
    try:
        BloFinExchangeAdapter = _blofin_adapter().BloFinExchangeAdapter
        from strategies import apply_strategy, get_strategy, list_strategies
        from close_registry_loader import (
            evaluate as close_evaluate,
            get_strategy as get_close_strategy,
            list_strategies as list_close_strategies,
        )
        from strategy_composition import (
            evaluate_open_close,
            finalize_decision,
            normalize_signal,
            parse_close_strategies,
            validate_close_strategy_names,
        )

        open_close_enabled = bool(open_strategy or close_strategies)
        configured_names = [open_strategy or strategy_name]
        for name in configured_names:
            get_strategy(name)
        close_names = parse_close_strategies(close_strategies)
        validate_close_strategy_names(
            close_names,
            get_strategy,
            get_close_strategy,
            list_strategies,
            list_close_strategies,
            required_platform="blofin-perps" if inst_type == "swap" else None,
        )

        adapter = BloFinExchangeAdapter()

        strategy_params = {}
        if strategy_name == "delta_neutral_funding" and inst_type == "swap":
            try:
                current_rate = adapter.get_funding_rate(symbol)
                history = adapter.get_funding_rate_history(symbol, limit=21 * 3)
                avg_rate = (sum(r["rate"] for r in history) / len(history)) if history else 0.0
                strategy_params = {
                    "current_funding_rate": current_rate,
                    "avg_funding_rate_7d": avg_rate,
                }
                print(f"Funding rate {symbol}: current={current_rate:.6f} avg7d={avg_rate:.6f}", file=sys.stderr)
            except Exception as e:
                print(f"Warning: failed to fetch funding rate: {e}", file=sys.stderr)

        # Enforce the measured per-timeframe LTF history floor after argparse
        # so a scheduler-appended --ohlcv-limit cannot shrink it again.
        eff_open_for_depth = (open_strategy or strategy_name or "").strip()
        if htf_timeframe and eff_open_for_depth == "ob_touch":
            ohlcv_limit = _ob_touch_ltf_limit(timeframe, ohlcv_limit)

        print(f"Fetching {symbol} {timeframe} from BloFin ({mode})...", file=sys.stderr)
        if inst_type == "spot":
            from spot_adapter import BloFinSpotExchangeAdapter
            spot_adapter = BloFinSpotExchangeAdapter()
            candles = spot_adapter.get_spot_ohlcv(symbol, interval=timeframe, limit=ohlcv_limit)
        elif inst_type == "swap":
            candles = adapter.get_perp_ohlcv(symbol, interval=timeframe, limit=ohlcv_limit)
        else:
            candles = adapter.get_ohlcv(symbol, interval=timeframe, limit=ohlcv_limit)

        if not candles or len(candles) < 30:
            print(json.dumps({
                "strategy": strategy_name,
                "symbol": symbol,
                "timeframe": timeframe,
                "signal": 0,
                "price": 0,
                "indicators": {},
                "mode": mode,
                "platform": "blofin",
                "timestamp": datetime.now(timezone.utc).isoformat(),
                "error": f"Insufficient data: {len(candles) if candles else 0} candles",
            }))
            sys.exit(1)

        df = _make_dataframe(candles)
        eff_open = (open_strategy or strategy_name or "").strip()
        df = closed_ltf_frame(df, eff_open, htf_timeframe)
        # Deep HTF frame for strategies with in-chart HTF confirmation
        # (ob_touch): fetched separately because the LTF window is too short
        # to resample enough HTF history. Injected as htf_df; the core drops
        # the forming HTF candle. Fetch failure here is fatal (fail-closed).
        if htf_timeframe and eff_open == "ob_touch":
            print(f"Fetching {symbol} {htf_timeframe} HTF from BloFin ({mode})...", file=sys.stderr)
            try:
                if inst_type == "spot":
                    from spot_adapter import BloFinSpotExchangeAdapter
                    htf_spot = BloFinSpotExchangeAdapter()
                    htf_candles = htf_spot.get_spot_ohlcv(symbol, interval=htf_timeframe, limit=htf_limit)
                else:
                    htf_candles = adapter.get_perp_ohlcv(symbol, interval=htf_timeframe, limit=htf_limit)
            except Exception as e:
                htf_candles = []
                print(f"HTF fetch error: {e}", file=sys.stderr)
            if not htf_candles or len(htf_candles) < 3:
                print(json.dumps({
                    "strategy": strategy_name,
                    "symbol": symbol,
                    "timeframe": timeframe,
                    "signal": 0,
                    "price": 0,
                    "indicators": {},
                    "mode": mode,
                    "platform": "blofin",
                    "timestamp": datetime.now(timezone.utc).isoformat(),
                    "error": f"Insufficient HTF data: {len(htf_candles) if htf_candles else 0} candles",
                }))
                sys.exit(1)
            strategy_params["htf_df"] = _make_dataframe(htf_candles)
        stdout_regime, live_regime, strategy_regime = prepare_check_regime(
            df,
            regime_enabled=regime_enabled,
            windows_spec=regime_windows_spec,
            atr_window=regime_atr_window,
            injected_payload_json=regime_payload_json,
        )
        strategy_params["regime"] = strategy_regime
        if strategy_params_override:
            merged = {**strategy_params_override, **strategy_params}
            strategy_params = merged
        decision = None
        close_context_warnings = []
        if open_close_enabled:
            market_ctx = {"mark_price": float(df["close"].iloc[-1])}
            atr_now = latest_atr(df, method=atr_method)
            if atr_now > 0:
                market_ctx["atr"] = atr_now
            if live_regime:
                market_ctx["regime"] = live_regime
            zscore_lookback, zscore_target = _configured_zscore_params(
                close_names, close_params_by_name
            )
            if zscore_lookback > 0 and zscore_target > 0:
                zscore = _closed_candle_zscore(df, timeframe, zscore_lookback)
                if zscore is not None:
                    market_ctx["zscore"] = zscore
                elif "zscore_target" in close_names:
                    close_context_warnings.append(
                        f"zscore_target needs {zscore_lookback} valid completed candles; no finite z-score was available"
                    )
            if "avwap_stop" in close_names and position_ctx:
                avwap = _position_anchored_avwap(
                    df, timeframe, position_ctx.get("opened_at_ms")
                )
                if avwap is not None:
                    market_ctx["avwap"] = avwap
                elif position_ctx.get("opened_at_ms"):
                    close_context_warnings.append(
                        "avwap_stop has no position-open anchored AVWAP yet; it requires at least one completed full candle after entry"
                    )
            if (
                "time_stop" in close_names
                and position_ctx
                and position_ctx.get("current_quantity", 0) > 0
                and position_ctx.get("opened_at_ms") is not None
            ):
                candle_bars_held = _completed_bars_held_from_candles(
                    df, timeframe, position_ctx["opened_at_ms"]
                )
                if candle_bars_held is not None:
                    position_ctx["bars_held"] = candle_bars_held
                else:
                    close_context_warnings.append(
                        "time_stop candle history does not cover the persisted position open time; using the scheduler's elapsed-bar count if available"
                    )
            if (
                "time_stop" in close_names
                and position_ctx
                and position_ctx.get("current_quantity", 0) > 0
                and position_ctx.get("bars_held") is None
            ):
                close_context_warnings.append(
                    "time_stop has no bars_held: the persisted position opened_at or strategy timeframe is unavailable"
                )
            if (
                position_ctx
                and position_ctx.get("current_quantity", 0) > 0
                and float(position_ctx.get("entry_atr", 0) or 0) <= 0
                and any(name in close_names for name in (
                    "tiered_tp_atr", "tiered_tp_atr_regime",
                    "tiered_tp_atr_live_regime_dynamic",
                    "trailing_tp_ratchet", "trailing_tp_ratchet_regime",
                ))
            ):
                close_context_warnings.append(
                    "the configured BloFin close requires Position.EntryATR, but the restored position has no valid entry ATR"
                )
            if (
                "tiered_tp_atr_live_regime" in close_names
                and "tiered_tp_atr_live_regime_dynamic" not in close_names
                and not live_regime
            ):
                close_context_warnings.append(
                    "tiered_tp_atr_live_regime has no live regime label for this candle"
                )
            evaluation = evaluate_open_close(
                apply_strategy,
                get_strategy,
                df,
                strategy_name,
                open_strategy,
                parse_close_strategies(close_strategies),
                position_side,
                strategy_params or None,
                position_ctx,
                close_evaluate=close_evaluate,
                market_ctx=market_ctx,
                close_params_by_name=close_params_by_name,
            )
            result_df = evaluation.open_result_df
            signal = evaluation.open_signal
            if "avwap_stop" in close_names and "avwap" not in market_ctx:
                open_result = evaluation.open_result_df
                open_avwap = None
                if not open_result.empty and "avwap" in open_result.columns:
                    try:
                        value = float(open_result["avwap"].iloc[-1])
                        open_avwap = value if math.isfinite(value) and value > 0 else None
                    except (TypeError, ValueError):
                        open_avwap = None
                if open_avwap is None:
                    close_context_warnings.append(
                        "avwap_stop has neither an open-strategy avwap column nor a position-anchored BloFin AVWAP"
                    )
        else:
            result_df = apply_strategy(strategy_name, df, strategy_params or None)
            signal = normalize_signal(result_df.iloc[-1].get("signal", 0))

        ensure_atr_indicator(result_df, method=atr_method)
        last = result_df.iloc[-1]
        price = float(last["close"])

        htf_info = {}
        htf_strategy_name = open_strategy or strategy_name
        if htf_filter_enabled and htf_strategy_name != "delta_neutral_funding":
            from htf_filter import htf_trend_filter, apply_htf_filter

            def _fetch_htf(sym, tf, limit):
                if inst_type == "swap":
                    candles = adapter.get_perp_ohlcv(sym, interval=tf, limit=limit)
                else:
                    candles = adapter.get_ohlcv(sym, interval=tf, limit=limit)
                return _make_dataframe(candles) if candles else None

            htf_info = htf_trend_filter(symbol, timeframe, _fetch_htf)
            original_signal = signal
            signal = apply_htf_filter(signal, htf_info.get("htf_trend", 0))
            if signal != original_signal:
                print(f"HTF filter: {original_signal} → {signal} (HTF trend={htf_info.get('htf_trend')})", file=sys.stderr)

        if open_close_enabled:
            decision = finalize_decision(evaluation, position_side, signal)
            signal = decision["signal"]

        try:
            if inst_type == "spot":
                from spot_adapter import BloFinSpotExchangeAdapter
                mid = BloFinSpotExchangeAdapter().get_spot_price(symbol)
            elif inst_type == "swap":
                mid = adapter.get_perp_price(symbol)
            else:
                mid = adapter.get_spot_price(symbol)
            if mid > 0:
                price = mid
        except Exception:
            pass

        indicators = {}
        skip_cols = {
            "open", "high", "low", "close", "volume",
            "timestamp", "signal", "position", "datetime",
        }
        for col in result_df.columns:
            if col in skip_cols:
                continue
            val = last.get(col)
            if val is not None:
                try:
                    fval = float(val)
                    if math.isfinite(fval):
                        indicators[col] = round(fval, 6)
                except (ValueError, TypeError):
                    pass

        if open_close_enabled:
            for key in ("zscore", "avwap"):
                value = market_ctx.get(key)
                if value is not None:
                    try:
                        fval = float(value)
                        if math.isfinite(fval):
                            indicators[key] = round(fval, 6)
                    except (ValueError, TypeError):
                        pass

        if htf_info:
            for k, v in htf_info.items():
                if isinstance(v, (int, float)):
                    indicators[k] = v

        output = {
            "strategy": strategy_name,
            "symbol": symbol,
            "timeframe": timeframe,
            "signal": signal,
            "price": round(price, 2),
            "indicators": indicators,
            "regime": stdout_regime,
            "mode": mode,
            "platform": "blofin",
            "timestamp": datetime.now(timezone.utc).isoformat(),
        }
        if decision:
            output.update(decision)
        if close_context_warnings:
            output["close_context_warnings"] = close_context_warnings
        print(json.dumps(output))

    except Exception as e:
        traceback.print_exc(file=sys.stderr)
        print(json.dumps({
            "strategy": strategy_name,
            "symbol": symbol,
            "timeframe": timeframe,
            "signal": 0,
            "price": 0,
            "indicators": {},
            "regime": None,
            "mode": mode,
            "platform": "blofin",
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "error": str(e),
        }))
        sys.exit(1)


def _order_response_data(result):
    """Normalize BloFin order responses whose ``data`` may be an object or list."""
    if not isinstance(result, dict):
        return {}
    data = result.get("data")
    if isinstance(data, dict):
        return data
    if isinstance(data, list):
        for item in data:
            if isinstance(item, dict):
                return item
    return result


def run_execute(symbol, side, size, mode, size_in_contracts=False, pos_side_hint="", is_close=False, leverage=0.0):
    """Place a live market order on BloFin."""
    if mode != "live":
        print(json.dumps({"error": "--execute requires --mode=live"}))
        sys.exit(1)

    try:
        adapter = _blofin_adapter().BloFinExchangeAdapter()
        is_buy = side.lower() == "buy"
        client_order_id = uuid.uuid4().hex if adapter.trade_account == "copy" else ""
        result = adapter.market_open(
            symbol, is_buy, size, inst_type="swap", size_in_contracts=size_in_contracts,
            pos_side_hint=pos_side_hint, is_close=is_close, leverage=leverage,
            client_order_id=client_order_id,
        )

        data = _order_response_data(result)
        oid = data.get("orderId") or data.get("ordId") or result.get("orderId") or result.get("ordId") or ""
        fill = {}
        copy_filled = False
        if adapter.trade_account == "copy" and (oid or client_order_id):
            # Copy order history can lag the place-order acknowledgement. Retry
            # briefly without letting one live execute approach scriptTimeout.
            got = adapter.get_copy_order_fill(
                str(oid or ""), f"{symbol}-USDT", tries=3,
                client_order_id=client_order_id, max_pages=1,
                request_timeout=4, retry_delay=1,
            )
            if got:
                fill = got
                copy_filled = True
        fill_cv = float(result.get("contract_value", 0) or 0)
        if not copy_filled:
            reported_size = (
                data.get("fillSz", 0)
                or data.get("accFillSz", 0)
                or data.get("filledSize", 0)
                or 0
            )
            fill = {
                "avg_px": float(data.get("fillPx", 0) or 0) or float(data.get("avgPx", 0) or 0),
                # A copy-order acknowledgement is not proof of a fill. Never
                # substitute requested size when Copy Trading history is late.
                "total_sz": float(reported_size or (0 if adapter.trade_account == "copy" else size)),
                "contract_value": fill_cv,
            }
            if oid:
                fill["oid"] = str(oid)
        elif fill_cv > 0:
            fill["contract_value"] = fill_cv
        if client_order_id:
            fill["client_order_id"] = client_order_id

        print(json.dumps({
            "execution": {
                "action": "buy" if is_buy else "sell",
                "symbol": symbol,
                "size": size,
                "fill": fill,
            },
            "platform": "blofin",
            "timestamp": datetime.now(timezone.utc).isoformat(),
        }))

    except Exception as e:
        errmsg = str(e)
        if errmsg.startswith("SKIP:"):
            print(json.dumps({
                "execution": None,
                "platform": "blofin",
                "timestamp": datetime.now(timezone.utc).isoformat(),
                "skipped": errmsg[5:].strip(),
            }))
            sys.exit(0)
        traceback.print_exc(file=sys.stderr)
        print(json.dumps({
            "execution": None,
            "platform": "blofin",
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "error": errmsg,
        }))
        sys.exit(1)


def main():
    if "--execute" in sys.argv:
        import argparse
        parser = argparse.ArgumentParser()
        parser.add_argument("--execute", action="store_true")
        parser.add_argument("--symbol", required=True)
        parser.add_argument("--side", required=True, choices=["buy", "sell"])
        parser.add_argument("--size", type=float, required=True)
        parser.add_argument("--mode", default="live")
        parser.add_argument("--size-in-contracts", action="store_true", default=False)
        parser.add_argument("--inst-type", default="swap", choices=["swap", "spot"])
        parser.add_argument("--sl-price", type=float, default=0.0)
        parser.add_argument("--pos-side-hint", default="")
        parser.add_argument("--is-close", action="store_true", default=False)
        parser.add_argument("--leverage", type=float, default=0.0)
        args = parser.parse_args()
        run_execute(args.symbol, args.side, args.size, args.mode, args.size_in_contracts, args.pos_side_hint, args.is_close, args.leverage)
    else:
        import argparse
        parser = argparse.ArgumentParser()
        parser.add_argument("strategy")
        parser.add_argument("symbol")
        parser.add_argument("timeframe")
        parser.add_argument("--mode", default="paper")
        parser.add_argument("--htf-filter", action="store_true", default=False)
        parser.add_argument("--regime-enabled", action="store_true", default=False)
        parser.add_argument("--regime-windows-spec-json", default="")
        parser.add_argument("--ohlcv-limit", type=int, default=200)
        parser.add_argument("--htf-timeframe", default="",
                            help="Deep HTF candles for in-chart HTF confirmation (ob_touch); "
                                 "empty disables the separate HTF fetch.")
        parser.add_argument("--htf-limit", type=int, default=300)
        parser.add_argument("--regime-atr-window", default="")
        parser.add_argument("--regime-payload-json", default=None)
        parser.add_argument("--regime-directional-window", default="")
        parser.add_argument("--atr-method", default="simple", choices=["simple", "wilder"])
        parser.add_argument("--inst-type", default="swap", choices=["swap", "spot"])
        parser.add_argument("--params", default=None)
        parser.add_argument("--open-strategy", default=None)
        parser.add_argument("--close-strategies", default=None)
        parser.add_argument("--strategy-refs", default=None)
        parser.add_argument("--position-side", default="")
        parser.add_argument("--position-avg-cost", type=float, default=None)
        parser.add_argument("--position-qty", type=float, default=None)
        parser.add_argument("--position-initial-qty", type=float, default=None)
        parser.add_argument("--position-entry-atr", type=float, default=None)
        parser.add_argument("--position-risk-anchor-price", type=float, default=None)
        parser.add_argument("--position-regime", default="")
        parser.add_argument("--position-regime-applied", default="")
        parser.add_argument("--position-regime-pending-label", default="")
        parser.add_argument("--position-regime-pending-count", type=int, default=0)
        parser.add_argument("--position-opened-at-ms", type=int, default=None)
        parser.add_argument("--position-bars-held", type=int, default=None)
        parser.add_argument("--mark-price", type=float, default=0.0)
        parser.add_argument("--probe-only", action="store_true")
        args = parser.parse_args()
        if args.probe_only:
            sys.exit(0)
        from strategy_composition import parse_strategy_refs_arg
        refs = parse_strategy_refs_arg(args.strategy_refs)
        open_strategy_name = refs["open_name"] if refs else args.open_strategy
        close_strategies_arg = refs["close_csv"] if refs else args.close_strategies
        params_override = refs["open_params"] if refs else (json.loads(args.params) if args.params else None)
        close_params_by_name = refs["close_params_by_name"] if refs else None
        position_ctx = _position_ctx_from_args(args)
        regime_windows_spec = parse_regime_windows_spec_json(args.regime_windows_spec_json or None)
        run_signal_check(
            args.strategy, args.symbol, args.timeframe, args.mode,
            args.htf_filter, args.inst_type, params_override,
            open_strategy_name, close_strategies_arg,
            args.position_side, position_ctx,
            regime_enabled=args.regime_enabled,
            regime_windows_spec=regime_windows_spec,
            ohlcv_limit=args.ohlcv_limit,
            regime_atr_window=args.regime_atr_window,
            regime_payload_json=args.regime_payload_json,
            atr_method=args.atr_method,
            close_params_by_name=close_params_by_name,
            htf_timeframe=args.htf_timeframe,
            htf_limit=args.htf_limit,
        )


if __name__ == "__main__":
    main()
