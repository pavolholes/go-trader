from __future__ import annotations

import os
import pathlib
import subprocess
import sys


def test_check_blofin_signal_cli_help_smoke():
    script = pathlib.Path(__file__).with_name("check_blofin.py")
    repo_root = pathlib.Path(__file__).resolve().parents[1]
    env = dict(os.environ)
    env["PYTHONDONTWRITEBYTECODE"] = "1"
    result = subprocess.run(
        [sys.executable, str(script), "--help"],
        cwd=repo_root,
        env=env,
        capture_output=True,
        text=True,
        timeout=20,
    )
    assert result.returncode == 0, result.stderr
    assert "--position-regime-pending-label" in result.stdout
    assert "--position-opened-at-ms" in result.stdout
