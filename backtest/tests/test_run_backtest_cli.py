import pandas as pd
import pytest

import run_backtest


@pytest.mark.parametrize("argv,expected", [
    (
        ["--registry", "futures", "--platform", "hyperliquid",
         "--strategy", "sma_crossover", "--mode", "single"],
        {"registry": "futures", "platform": "hyperliquid"},
    ),
    (
        ["--mode", "optimize", "--strategy", "sma_crossover",
         "--sweep-close", "--optimize-metric", "dd_adjusted_return",
         "--direction", "long"],
        {"sweep_close": True, "optimize_metric": "dd_adjusted_return",
         "direction": "long", "close_stacks_json": None},
    ),
])
def test_build_parser_accepts(argv, expected):
    args = run_backtest._build_parser().parse_args(argv)
    for field, want in expected.items():
        assert getattr(args, field) == want


@pytest.mark.parametrize("argv", [
    ["--platform", "mystery-exchange"],
    ["--registry", "options"],
    ["--optimize-metric", "alpha_decay"],
])
def test_build_parser_rejects_unknown_choice(argv):
    parser = run_backtest._build_parser()
    with pytest.raises(SystemExit):
        parser.parse_args(argv)


def test_run_single_backtest_threads_platform_to_backtester(monkeypatch):
    seen = {}

    class SpyBacktester:
        def __init__(self, initial_capital, platform="binanceus", **kwargs):
            seen["platform"] = platform
            seen["capital"] = initial_capital
            self.commission_pct = 0.123

        def run(self, df, **kwargs):
            return {
                "strategy_name": kwargs.get("strategy_name"),
                "symbol": "BTC/USDT",
                "timeframe": "1d",
                "start_date": str(df.index[0]),
                "end_date": str(df.index[-1]),
                "initial_capital": 1000.0,
                "final_capital": 1000.0,
                "total_return_pct": 0.0,
                "annual_return_pct": 0.0,
                "sharpe_ratio": 0.0,
                "sortino_ratio": 0.0,
                "max_drawdown_pct": 0.0,
                "calmar_ratio": 0.0,
                "volatility_pct": 0.0,
                "win_rate": 0.0,
                "profit_factor": 0.0,
                "total_trades": 0,
                "avg_win_pct": 0.0,
                "avg_loss_pct": 0.0,
                "trades": [],
                "params": {},
            }

    df = pd.DataFrame(
        {"open": [100] * 60, "high": [101] * 60, "low": [99] * 60,
         "close": [100] * 60, "volume": [1000] * 60},
        index=pd.date_range("2024-01-01", periods=60, freq="D"),
    )
    monkeypatch.setattr(run_backtest, "Backtester", SpyBacktester)
    monkeypatch.setattr(run_backtest, "load_cached_data",
                        lambda *a, **kw: (seen.setdefault("cache_store", kw.get("store")), df)[1])

    result = run_backtest.run_single_backtest(
        strategy_name="sma_crossover",
        symbol="BTC/USDT",
        timeframe="1d",
        since="2024-01-01",
        capital=777.0,
        platform="robinhood",
        registry="spot",
    )
    assert result is not None
    assert seen["platform"] == "robinhood", (
        f"platform did not thread through to Backtester — got {seen}"
    )
    assert seen["capital"] == 777.0
    assert seen["cache_store"] is False, "save=False must not populate the OHLCV cache"


def test_blofin_backtest_defaults_tp_enabled_like_live(monkeypatch):
    seen = {}

    class SpyBacktester:
        def __init__(self, initial_capital, platform="binanceus", close_strategies=None, **kwargs):
            seen["platform"] = platform
            seen["closes"] = close_strategies
            self.commission_pct = 0.0

        def run(self, df, **kwargs):
            return {
                "strategy_name": "momentum", "symbol": "BTC/USDT", "timeframe": "1h",
                "start_date": str(df.index[0]), "end_date": str(df.index[-1]),
                "initial_capital": 1000.0, "final_capital": 1000.0,
                "total_return_pct": 0.0, "annual_return_pct": 0.0,
                "sharpe_ratio": 0.0, "sortino_ratio": 0.0,
                "max_drawdown_pct": 0.0, "calmar_ratio": 0.0,
                "volatility_pct": 0.0, "win_rate": 0.0, "profit_factor": 0.0,
                "total_trades": 0, "avg_win_pct": 0.0, "avg_loss_pct": 0.0,
                "trades": [], "params": {},
            }

    df = pd.DataFrame(
        {"open": [100] * 60, "high": [101] * 60, "low": [99] * 60,
         "close": [100] * 60, "volume": [1000] * 60},
        index=pd.date_range("2026-01-01", periods=60, freq="h"),
    )
    monkeypatch.setattr(run_backtest, "Backtester", SpyBacktester)
    monkeypatch.setattr(run_backtest, "load_cached_data", lambda *a, **kw: df)
    run_backtest.run_single_backtest(
        strategy_name="momentum",
        symbol="BTC/USDT",
        timeframe="1h",
        since="2026-01-01",
        platform="blofin",
        registry="futures",
        close_strategies=[{"name": "tiered_tp_atr_regime", "params": {}}],
        save=False,
    )
    assert seen["platform"] == "blofin"
    assert seen["closes"][0]["params"]["tp_enabled"] is True


def test_backtester_imports_under_script_style_sys_path(tmp_path):
    import os
    import subprocess
    import sys

    backtest_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
    snippet = tmp_path / "script_style_import.py"
    snippet.write_text(
        "import sys\n"
        f"sys.path.insert(0, {backtest_dir!r})\n"
        "import backtester\n"
        "backtester.Backtester()\n"
        "print('OK')\n"
    )
    proc = subprocess.run(
        [sys.executable, str(snippet)],
        cwd=tmp_path, capture_output=True, text=True, timeout=120,
    )
    assert proc.returncode == 0, proc.stderr
    assert "OK" in proc.stdout


def _spy_single(monkeypatch):
    seen = {}

    def spy(*args, **kwargs):
        seen.setdefault("calls", []).append(kwargs)
        return None

    monkeypatch.setattr(run_backtest, "run_single_backtest", spy)
    return seen


@pytest.mark.parametrize("extra_argv", [
    ["--mode", "single"],
    ["--mode", "compare"],
    ["--mode", "multi", "--symbols", "BTC/USDT"],
])
def test_mode_threads_direction(monkeypatch, extra_argv):
    seen = _spy_single(monkeypatch)
    monkeypatch.setattr("sys.argv", [
        "run_backtest.py", *extra_argv,
        "--strategy", "sma_crossover", "--direction", "short",
    ])
    run_backtest.main()
    assert seen["calls"][0]["direction"] == "short"


def test_direction_both_without_close_rejected_before_running(monkeypatch):
    seen = _spy_single(monkeypatch)
    monkeypatch.setattr("sys.argv", [
        "run_backtest.py", "--mode", "single",
        "--strategy", "sma_crossover", "--direction", "both",
    ])
    with pytest.raises(SystemExit):
        run_backtest.main()
    assert "calls" not in seen


def test_direction_both_with_close_strategy_threads_through(monkeypatch):
    seen = _spy_single(monkeypatch)
    monkeypatch.setattr("sys.argv", [
        "run_backtest.py", "--mode", "single",
        "--strategy", "sma_crossover", "--direction", "both",
        "--close-strategy", "tiered_tp_atr",
    ])
    run_backtest.main()
    assert seen["calls"][0]["direction"] == "both"
    assert seen["calls"][0]["close_strategies"] == [
        {"name": "tiered_tp_atr", "params": {}}]


def test_direction_short_rejected_in_optimize_mode(monkeypatch):
    seen = {}
    monkeypatch.setattr(run_backtest, "run_walk_forward",
                        lambda *a, **kw: seen.setdefault("hit", True))
    for extra in ([], ["--sweep-close"],
                  ["--close-strategy", "tiered_tp_atr"]):
        monkeypatch.setattr("sys.argv", [
            "run_backtest.py", "--mode", "optimize",
            "--strategy", "sma_crossover", "--direction", "short", *extra,
        ])
        with pytest.raises(SystemExit):
            run_backtest.main()
    assert "hit" not in seen


def test_direction_both_with_default_sweep_grid_rejected(monkeypatch):
    seen = {}
    monkeypatch.setattr(run_backtest, "run_walk_forward",
                        lambda *a, **kw: seen.setdefault("hit", True))
    monkeypatch.setattr("sys.argv", [
        "run_backtest.py", "--mode", "optimize",
        "--strategy", "sma_crossover", "--sweep-close",
        "--direction", "both",
    ])
    with pytest.raises(SystemExit):
        run_backtest.main()
    assert "hit" not in seen


def test_direction_both_with_close_only_stacks_json_reaches_walk_forward(
        monkeypatch, tmp_path):
    import json
    specs = tmp_path / "stacks.json"
    specs.write_text(json.dumps([
        {"close": {"name": "tiered_tp_atr", "params": {}}},
    ]))
    seen = {}

    def spy_wf(*args, **kwargs):
        seen.update(kwargs)

    monkeypatch.setattr(run_backtest, "run_walk_forward", spy_wf)
    monkeypatch.setattr("sys.argv", [
        "run_backtest.py", "--mode", "optimize",
        "--strategy", "sma_crossover",
        "--close-stacks-json", str(specs),
        "--direction", "both",
    ])
    run_backtest.main()
    assert seen["direction"] == "both"
    assert seen["close_stack_grid"]
    assert all(s.get("close_strategies") for s in seen["close_stack_grid"])


def test_run_walk_forward_threads_close_stack_grid(monkeypatch):
    seen = {}

    def spy_wfo(df, strategy_name, param_ranges, **kwargs):
        seen.update(kwargs)
        return {"error": "spy", "strategy": strategy_name}

    monkeypatch.setattr(run_backtest, "walk_forward_optimize", spy_wfo)
    monkeypatch.setattr(
        run_backtest, "load_cached_data",
        lambda *a, **k: pd.DataFrame(
            {"open": [1.0] * 300, "high": [1.0] * 300, "low": [1.0] * 300,
             "close": [1.0] * 300, "volume": [1.0] * 300},
            index=pd.date_range("2024-01-01", periods=300, freq="D")))

    grid = [{"label": "baseline", "close_strategies": [],
             "stop_loss_atr_mult": None, "trailing_stop_atr_mult": None}]
    run_backtest.run_walk_forward(
        "sma_crossover", close_stack_grid=grid,
        optimize_metric="dd_adjusted_return", direction="long")
    assert seen["close_stack_grid"] == grid
    assert seen["optimize_metric"] == "dd_adjusted_return"
    assert seen["direction"] == "long"


def test_config_defaults_thread_perps_runtime_inputs(tmp_path, monkeypatch):
    import json

    config = {
        "config_version": 19,
        "regime": {"enabled": False},
        "strategies": [{
            "id": "live-sma-eth-30m",
            "type": "perps",
            "platform": "blofin",
            "script": "shared_scripts/check_blofin.py",
            "args": ["sma_crossover", "ETH", "30m", "--mode=live"],
            "capital": 100.0,
            "leverage": 75,
            "margin_per_trade_usd": 10.0,
            "max_drawdown_pct": 50.0,
            "close_strategy": {
                "name": "tiered_tp_atr_regime",
                "params": {"sl_atr_mult": 2.5, "use_defaults": True},
            },
        }],
    }
    config_path = tmp_path / "blofin-config.json"
    config_path.write_text(json.dumps(config), encoding="utf-8")
    seen = {}

    def spy_run_single(*args, **kwargs):
        seen["args"] = args
        seen["kwargs"] = kwargs

    monkeypatch.setattr(run_backtest, "run_single_backtest", spy_run_single)
    monkeypatch.setattr("sys.argv", [
        "run_backtest.py", "--mode", "single",
        "--config", str(config_path), "--strategy", "live-sma-eth-30m",
    ])

    run_backtest.main()

    assert seen["args"] == ("sma_crossover", "ETH", "30m", "2022-01-01", 100.0)
    assert seen["kwargs"]["registry"] == "futures"
    assert seen["kwargs"]["platform"] == "blofin"
    assert seen["kwargs"]["margin_per_trade_usd"] == 10.0
    assert seen["kwargs"]["leverage"] == 75.0
    assert seen["kwargs"]["circuit_breaker_max_drawdown_pct"] == 50.0
    assert seen["kwargs"]["close_strategies"][0]["params"]["sl_atr_mult"] == 2.5
