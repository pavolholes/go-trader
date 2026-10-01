import importlib.util
import pathlib
import sys

ROOT = pathlib.Path(__file__).parent.parent
for path in (ROOT, ROOT / "shared_tools"):
    if str(path) not in sys.path:
        sys.path.insert(0, str(path))


def _load(name, relpath):
    path = ROOT / relpath
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


_BLOFIN = _load("_ob_touch_depth_blofin", "shared_scripts/check_blofin.py")
_TOPSTEP = _load("_ob_touch_depth_topstep", "shared_scripts/check_topstep.py")


def test_both_checks_apply_measured_timeframe_floor():
    for module in (_BLOFIN, _TOPSTEP):
        assert module._ob_touch_ltf_limit("5m", 200) == 2000
        assert module._ob_touch_ltf_limit("15m", 200) == 1200


def test_floor_does_not_lower_explicit_deeper_request():
    for module in (_BLOFIN, _TOPSTEP):
        assert module._ob_touch_ltf_limit("5m", 2500) == 2500


def test_non_ob_touch_timeframes_keep_legacy_default_floor():
    for module in (_BLOFIN, _TOPSTEP):
        assert module._ob_touch_ltf_limit("1h", 200) == 600


def test_invalid_requested_limit_uses_timeframe_floor():
    for module in (_BLOFIN, _TOPSTEP):
        assert module._ob_touch_ltf_limit("5m", "invalid") == 2000
