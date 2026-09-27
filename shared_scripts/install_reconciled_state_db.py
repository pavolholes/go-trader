#!/usr/bin/env python3
"""Install a validated, offline BloFin-reconciled state.db with rollback."""

import argparse
import json
import os
import sqlite3


def inspect_db(path):
    conn = sqlite3.connect("file:" + os.path.abspath(path) + "?mode=ro", uri=True)
    ids = ("live-*",)
    result = {
        "integrity": conn.execute("PRAGMA integrity_check").fetchone()[0],
        "trade_rows": conn.execute("SELECT COUNT(*) FROM trades WHERE strategy_id GLOB ?", ids).fetchone()[0],
        "close_fills": conn.execute("SELECT COUNT(*) FROM trades WHERE strategy_id GLOB ? AND is_close=1", ids).fetchone()[0],
        "closed_positions": conn.execute("SELECT COUNT(*) FROM closed_positions WHERE strategy_id GLOB ?", ids).fetchone()[0],
        "open_positions": conn.execute("SELECT COUNT(*) FROM positions WHERE strategy_id GLOB ?", ids).fetchone()[0],
        "realized": float(conn.execute("SELECT COALESCE(SUM(realized_pnl),0) FROM trades WHERE strategy_id GLOB ? AND is_close=1", ids).fetchone()[0]),
    }
    conn.close()
    return result


def validate_stage(path, expected):
    result = inspect_db(path)
    if result["integrity"] != "ok":
        raise RuntimeError("staging database integrity check failed")
    for key in ("trade_rows", "close_fills", "closed_positions", "open_positions"):
        if result[key] != expected[key]:
            raise RuntimeError("staging %s = %s, expected %s" % (key, result[key], expected[key]))
    if abs(result["realized"] - expected["realized"]) > 0.00001:
        raise RuntimeError("staging realized = %.8f, expected %.8f" % (result["realized"], expected["realized"]))
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data-dir", required=True)
    parser.add_argument("--stage", required=True, help="staged DB basename inside data-dir")
    parser.add_argument("--backup-tag", required=True, help="unique suffix for the original DB and WAL sidecars")
    parser.add_argument("--consistent-backup", required=True, help="consistent SQLite backup basename")
    parser.add_argument("--expected-trades", required=True, type=int)
    parser.add_argument("--expected-close-fills", required=True, type=int)
    parser.add_argument("--expected-closed-positions", required=True, type=int)
    parser.add_argument("--expected-open-positions", required=True, type=int)
    parser.add_argument("--expected-realized", required=True, type=float)
    parser.add_argument("--expected-original-trades", required=True, type=int)
    parser.add_argument("--expected-original-closed", required=True, type=int)
    args = parser.parse_args()
    expected = {
        "trade_rows": args.expected_trades,
        "close_fills": args.expected_close_fills,
        "closed_positions": args.expected_closed_positions,
        "open_positions": args.expected_open_positions,
        "realized": args.expected_realized,
    }

    data_dir = os.path.abspath(args.data_dir)
    db_path = os.path.join(data_dir, "state.db")
    stage_path = os.path.join(data_dir, args.stage)
    raw_backup = db_path + ".pre-blofin-" + args.backup_tag + ".raw"
    archived_sidecars = {
        db_path + "-wal": db_path + ".pre-blofin-" + args.backup_tag + ".wal",
        db_path + "-shm": db_path + ".pre-blofin-" + args.backup_tag + ".shm",
    }

    for path in (db_path, stage_path):
        if not os.path.isfile(path):
            raise FileNotFoundError(path)
    if os.path.exists(raw_backup) or any(os.path.exists(dest) for dest in archived_sidecars.values()):
        raise FileExistsError("backup destination already exists for tag " + args.backup_tag)

    stage_report = validate_stage(stage_path, expected)
    original_report = inspect_db(db_path)
    if original_report["integrity"] != "ok" or original_report["trade_rows"] != args.expected_original_trades or original_report["closed_positions"] != args.expected_original_closed:
        raise RuntimeError("original DB no longer matches the reviewed pre-reconciliation snapshot")

    moved = []
    installed = False
    try:
        os.replace(db_path, raw_backup)
        moved.append((raw_backup, db_path))
        for source, destination in archived_sidecars.items():
            if os.path.exists(source):
                os.replace(source, destination)
                moved.append((destination, source))
        os.replace(stage_path, db_path)
        installed = True
        final_report = validate_stage(db_path, expected)
    except Exception:
        if installed and os.path.exists(db_path):
            os.replace(db_path, stage_path)
        for moved_path, original_path in reversed(moved):
            if os.path.exists(moved_path):
                os.replace(moved_path, original_path)
        raise

    print(json.dumps({
        "installed_db": db_path,
        "raw_original_backup": raw_backup,
        "archived_sidecars": [dest for dest in archived_sidecars.values() if os.path.exists(dest)],
        "consistent_backup": os.path.join(data_dir, args.consistent_backup),
        "stage_validation": stage_report,
        "installed_validation": final_report,
    }, separators=(",", ":")))


if __name__ == "__main__":
    main()
