import importlib.util
from pathlib import Path

import pytest


SCRIPT = Path(__file__).with_name("fetch_blofin_copy_balance.py")
SPEC = importlib.util.spec_from_file_location("fetch_blofin_copy_balance", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


@pytest.mark.parametrize(
    ("payload", "want", "want_error"),
    [
        (
            {"totalEquity": "390.535", "ts": "1790533200000", "details": [{"available": "354.1"}]},
            (390.535, 354.1, 1790533200000),
            False,
        ),
        ({"totalEquity": "390.5", "ts": "0"}, None, True),
        ({"totalEquity": "0", "ts": "1790533200000"}, None, True),
        ({"totalEquity": "NaN", "ts": "1790533200000"}, None, True),
        ([], None, True),
    ],
)
def test_parse_copy_perps_equity_requires_positive_timestamped_total_equity(payload, want, want_error):
    if want_error:
        with pytest.raises(RuntimeError):
            MODULE.parse_copy_perps_equity(payload)
        return
    assert MODULE.parse_copy_perps_equity(payload) == pytest.approx(want)
