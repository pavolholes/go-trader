
import pathlib
import sys

_SHARED_TOOLS = pathlib.Path(__file__).parent.parent / "shared_tools"
_REPO_ROOT = _SHARED_TOOLS.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))
if str(_SHARED_TOOLS) not in sys.path:
    sys.path.insert(0, str(_SHARED_TOOLS))

from shared_tools.conftest import load_module

_CHECK_BLOFIN = load_module(
    "_check_blofin_inst_type_test",
    pathlib.Path(__file__).parent / "check_blofin.py",
)
_detect_inst_type = _CHECK_BLOFIN._detect_inst_type


def test_equals_form():
    argv = ["check_blofin.py", "ob_touch", "BTC", "15m", "--inst-type=swap"]
    assert _detect_inst_type(argv) == "swap"


def test_space_form_spot():
    argv = ["check_blofin.py", "ob_touch", "BTC", "15m", "--mode=paper",
            "--inst-type", "spot", "--htf-timeframe", "1h"]
    assert _detect_inst_type(argv) == "spot"


def test_space_form_swap():
    argv = ["check_blofin.py", "ob_touch", "BTC", "15m", "--inst-type", "swap"]
    assert _detect_inst_type(argv) == "swap"


def test_default_is_swap():
    argv = ["check_blofin.py", "ob_touch", "BTC", "15m", "--mode=paper"]
    assert _detect_inst_type(argv) == "swap"


def test_dangling_flag_falls_back_to_swap():
    argv = ["check_blofin.py", "ob_touch", "BTC", "15m", "--inst-type"]
    assert _detect_inst_type(argv) == "swap"
