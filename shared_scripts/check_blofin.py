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
    return ctx


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
        validate_close_strategy_names(
            parse_close_strategies(close_strategies),
            get_strategy,
            get_close_strategy,
            list_strategies,
            list_close_strategies,
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
        if open_close_enabled:
            market_ctx = {"mark_price": float(df["close"].iloc[-1])}
            atr_now = latest_atr(df, method=atr_method)
            if atr_now > 0:
                market_ctx["atr"] = atr_now
            if live_regime:
                market_ctx["regime"] = live_regime
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
