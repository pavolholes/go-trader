#!/usr/bin/env python3
"""Rebuild the current live BloFin perps ledger from Copy Trading history.

This tool is intentionally staging-only: --apply writes a new database at
--output-db and never changes --db. Review the emitted reconciliation totals
before replacing the stopped service's database with the staged file.
"""

import argparse
import json
import os
import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation


sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "platforms", "blofin"))

ZERO = Decimal("0")
POSITION_HISTORY_PATH = "/api/v1/copytrading/trade/position-history-by-order"
POSITION_DETAILS_PATH = "/api/v1/copytrading/account/positions-details-by-order"
ORDER_HISTORY_PATH = "/api/v1/copytrading/trade/orders-history"


def dec(value, default=ZERO):
    try:
        return Decimal(str(value)) if value not in (None, "") else default
    except (InvalidOperation, ValueError, TypeError):
        return default


def timestamp_ms(value):
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def iso_from_ms(value):
    return datetime.fromtimestamp(timestamp_ms(value) / 1000, timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def iso_to_ms(value):
    if not value:
        return 0
    parsed = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return int(parsed.timestamp() * 1000)


def copy_position_history(adapter):
    rows = []
    seen = set()
    cursor = None
    for _ in range(100):
        params = {"limit": "20"}
        if cursor:
            params["before"] = cursor
        batch = adapter._private_get(POSITION_HISTORY_PATH, params).get("data", [])
        fresh = [row for row in batch if row.get("orderId") and row.get("orderId") not in seen]
        if not fresh:
            return rows
        rows.extend(fresh)
        seen.update(row["orderId"] for row in fresh)
        cursor = fresh[-1]["orderId"]
        if len(batch) < 20:
            return rows
    raise RuntimeError("BloFin Copy Trading position history exceeded 100 pages")


def copy_open_positions_by_order(adapter):
    rows = adapter._private_get("/api/v1/copytrading/account/positions-by-order", {}).get("data", [])
    return rows if isinstance(rows, list) else []


def copy_order_history(adapter, inst_ids, since_ms):
    orders = {}
    for inst_id in sorted(inst_ids):
        cursor = None
        seen = set()
        for _ in range(100):
            params = {"instId": inst_id, "limit": "20"}
            if cursor:
                params["before"] = cursor
            batch = adapter._private_get(ORDER_HISTORY_PATH, params).get("data", [])
            fresh = [row for row in batch if row.get("orderId") and row.get("orderId") not in seen]
            if not fresh:
                break
            for row in fresh:
                seen.add(row["orderId"])
                if timestamp_ms(row.get("createTime")) >= since_ms:
                    orders[str(row["orderId"])] = row
            oldest_ms = min(timestamp_ms(row.get("createTime")) for row in fresh)
            if len(batch) < 20 or oldest_ms < since_ms:
                break
            cursor = fresh[-1]["orderId"]
        else:
            raise RuntimeError("BloFin order history exceeded 100 pages for " + inst_id)
    return orders


def load_source_rows(path, strategy_ids):
    uri = "file:" + os.path.abspath(path) + "?mode=ro"
    conn = sqlite3.connect(uri, uri=True)
    conn.row_factory = sqlite3.Row
    ids = sorted(strategy_ids)
    placeholders = ",".join("?" for _ in ids)
    trades = [dict(row) for row in conn.execute(
        "SELECT strategy_id,timestamp,symbol,position_id,side,quantity,price,value,trade_type,details,"
        "exchange_order_id,exchange_fee,is_close,realized_pnl,pnl_gross,fee_source "
        "FROM trades WHERE strategy_id IN (" + placeholders + ") ORDER BY timestamp,rowid", ids
    )]
    positions = [dict(row) for row in conn.execute(
        "SELECT strategy_id,symbol,quantity,side FROM positions WHERE strategy_id IN (" + placeholders + ")", ids
    )]
    old_closed = [dict(row) for row in conn.execute(
        "SELECT strategy_id,symbol,opened_at,closed_at FROM closed_positions WHERE strategy_id IN (" + placeholders + ")", ids
    )]
    conn.close()
    return trades, positions, old_closed


def config_live_blofin_strategies(config_path):
    with open(config_path, "r", encoding="utf-8") as stream:
        config = json.load(stream)
    strategies = {}
    by_symbol = defaultdict(list)
    for item in config.get("strategies", []):
        args = item.get("args") or []
        if item.get("platform") != "blofin" or item.get("type") != "perps" or "--mode=live" not in args:
            continue
        symbol = str(item.get("symbol") or (args[1] if len(args) > 1 else "")).upper()
        if not symbol:
            continue
        strategies[item["id"]] = {"id": item["id"], "symbol": symbol, "config": item}
        by_symbol[symbol].append(item["id"])
    if not strategies:
        raise RuntimeError("No live BloFin perps strategies found in config")
    return strategies, by_symbol


def match_strategy(position, trades, strategies, by_symbol):
    order_id = str(position.get("orderId", ""))
    symbol = str(position.get("instId", "")).split("-", 1)[0].upper()
    direct = {row["strategy_id"] for row in trades if str(row.get("exchange_order_id", "")) == order_id}
    if len(direct) == 1:
        return next(iter(direct))
    if len(direct) > 1:
        raise RuntimeError("orderId maps to multiple strategies: " + order_id)

    opened_at = timestamp_ms(position.get("createTime"))
    side = str(position.get("side", "")).lower()
    candidates = []
    for row in trades:
        if str(row.get("symbol", "")).upper() != symbol or str(row.get("side", "")).lower() != side:
            continue
        delta = abs(iso_to_ms(row.get("timestamp")) - opened_at)
        if delta <= 120_000:
            candidates.append((delta, row["strategy_id"]))
    if candidates:
        candidates.sort()
        nearest = candidates[0][0]
        nearest_ids = {strategy_id for delta, strategy_id in candidates if delta == nearest}
        if len(nearest_ids) == 1:
            return next(iter(nearest_ids))

    symbol_candidates = by_symbol.get(symbol, [])
    if len(symbol_candidates) == 1:
        return symbol_candidates[0]
    raise RuntimeError("cannot safely map BloFin order " + order_id + " (" + symbol + ") to a strategy")


def instrument_contract_value(adapter, inst_id, cache):
    if inst_id not in cache:
        rows = adapter._public_get("/api/v1/market/instruments", {"instId": inst_id}).get("data", [])
        if not rows:
            raise RuntimeError("BloFin instrument not found: " + inst_id)
        value = dec(rows[0].get("contractValue"))
        if value <= 0:
            raise RuntimeError("invalid contractValue for " + inst_id)
        cache[inst_id] = value
    return cache[inst_id]


def matching_order(order_history, inst_id, when_ms, side, quantity, price):
    quantity = dec(quantity)
    price = dec(price)
    candidates = []
    for row in order_history.values():
        if str(row.get("instId", "")).upper() != inst_id.upper():
            continue
        if str(row.get("side", "")).lower() != str(side).lower():
            continue
        if abs(timestamp_ms(row.get("createTime")) - when_ms) > 2_000:
            continue
        if abs(dec(row.get("filledSize")) - quantity) > max(Decimal("0.000001"), quantity * Decimal("0.000001")):
            continue
        if abs(dec(row.get("averagePrice")) - price) > max(Decimal("0.00000001"), price * Decimal("0.00000001")):
            continue
        candidates.append(row)
    if len(candidates) != 1:
        raise RuntimeError("expected one order-history match for %s %s at %s; got %s" % (inst_id, side, when_ms, len(candidates)))
    return candidates[0]


def build_position_plan(adapter, position, trades, strategies, by_symbol, contract_values, order_history):
    order_id = str(position["orderId"])
    inst_id = str(position.get("instId", ""))
    symbol = inst_id.split("-", 1)[0].upper()
    strategy_id = match_strategy(position, trades, strategies, by_symbol)
    if strategies[strategy_id]["symbol"] != symbol:
        raise RuntimeError("matched strategy symbol mismatch for order " + order_id)

    quantity = dec(position.get("positions"))
    open_price = dec(position.get("openAveragePrice"))
    position_pnl = dec(position.get("pnl"))
    open_ms = timestamp_ms(position.get("createTime"))
    close_ms = timestamp_ms(position.get("closeTime"))
    if quantity <= 0 or open_price <= 0 or not open_ms or not close_ms:
        raise RuntimeError("incomplete closed position history for order " + order_id)

    detail = adapter._private_get(POSITION_DETAILS_PATH, {"orderId": order_id}).get("data", {})
    closes = detail.get("orderList", [])
    if not closes:
        raise RuntimeError("no close fills returned for order " + order_id)
    close_qty = sum((dec(row.get("size")) for row in closes), ZERO)
    if abs(close_qty - quantity) > max(Decimal("0.000001"), quantity * Decimal("0.000001")):
        raise RuntimeError("close quantity mismatch for order " + order_id + ": " + str(close_qty) + " != " + str(quantity))

    raw_close_pnl = [dec(row.get("realizedPnl")) for row in closes]
    residual = position_pnl - sum(raw_close_pnl, ZERO)
    if abs(residual) > Decimal("0.10"):
        raise RuntimeError("close-fill PnL differs from position PnL by " + str(residual) + " for order " + order_id)
    raw_close_pnl[-1] += residual

    contract_value = instrument_contract_value(adapter, inst_id, contract_values)
    open_order = order_history.get(order_id)
    if open_order is None:
        open_order = matching_order(
            order_history, inst_id, open_ms, position.get("side"), quantity, open_price
        )
    if str(open_order.get("instId", "")).upper() != inst_id.upper():
        raise RuntimeError("opening order instrument mismatch for " + order_id)
    if abs(dec(open_order.get("filledSize")) - quantity) > max(Decimal("0.000001"), quantity * Decimal("0.000001")):
        raise RuntimeError("opening order quantity mismatch for " + order_id)
    if abs(dec(open_order.get("averagePrice")) - open_price) > max(Decimal("0.00000001"), open_price * Decimal("0.00000001")):
        raise RuntimeError("opening order price mismatch for " + order_id)
    open_fee = dec(open_order.get("fee"))

    position_id = strategy_id + ":" + symbol + ":blofin:" + order_id
    opened_iso = iso_from_ms(open_ms)
    closed_iso = iso_from_ms(close_ms)
    open_side = str(position.get("side", "")).lower()
    if open_side not in ("buy", "sell"):
        raise RuntimeError("invalid opening side for order " + order_id)
    position_side = str(position.get("positionSide", "")).lower()
    if position_side not in ("long", "short"):
        raise RuntimeError("invalid positionSide for order " + order_id)

    events = [{
        "timestamp": opened_iso,
        "strategy_id": strategy_id,
        "symbol": symbol,
        "position_id": position_id,
        "side": open_side,
        "quantity": float(quantity),
        "price": float(open_price),
        "value": float(quantity * open_price * contract_value),
        "trade_type": "perps",
        "details": "BloFin historical opening fill; orderId=" + order_id,
        "exchange_order_id": order_id,
        "exchange_fee": float(open_fee),
        "is_close": 0,
        "realized_pnl": 0.0,
        "pnl_gross": 1,
        "fee_source": "userfills",
    }]

    for index, close in enumerate(closes):
        close_id = str(close.get("closeOrderId", ""))
        close_qty_leg = dec(close.get("size"))
        close_px = dec(close.get("averagePrice"))
        close_fee = dec(close.get("fee"))
        close_side = str(close.get("side", "")).lower()
        close_ms_leg = timestamp_ms(close.get("orderTime"))
        if not close_id or close_qty_leg <= 0 or close_px <= 0 or not close_ms_leg or close_side not in ("buy", "sell"):
            raise RuntimeError("incomplete close fill for position order " + order_id)
        close_order = matching_order(order_history, inst_id, close_ms_leg, close_side, close_qty_leg, close_px)
        history_fee = dec(close_order.get("fee"))
        if abs(history_fee - close_fee) > Decimal("0.000001"):
            raise RuntimeError("close fee mismatch for order %s/%s" % (order_id, close_order.get("orderId")))
        events.append({
            "timestamp": iso_from_ms(close_ms_leg),
            "strategy_id": strategy_id,
            "symbol": symbol,
            "position_id": position_id,
            "side": close_side,
            "quantity": float(close_qty_leg),
            "price": float(close_px),
            "value": float(close_qty_leg * close_px * contract_value),
            "trade_type": "perps",
            "details": "BloFin historical close fill; positionOrderId=%s detailsCloseId=%s" % (order_id, close_id),
            "exchange_order_id": str(close_order["orderId"]),
            "exchange_fee": float(history_fee),
            "is_close": 1,
            "realized_pnl": float(raw_close_pnl[index]),
            "pnl_gross": 1,
            "fee_source": "userfills",
        })

    summary = {
        "order_id": order_id,
        "strategy_id": strategy_id,
        "symbol": symbol,
        "position_side": position_side,
        "quantity": float(quantity),
        "contract_value": float(contract_value),
        "open_price": float(open_price),
        "opened_at": opened_iso,
        "closed_at": closed_iso,
        "close_price": float(dec(position.get("closeAveragePrice"))),
        "realized_pnl": float(position_pnl),
        "close_reason": str(position.get("closeType", "close")),
        "close_fills": len(closes),
        "close_fee": float(sum((dec(row.get("fee")) for row in closes), ZERO)),
        "open_fee": float(open_fee),
        "open_exchange_order_id": str(open_order["orderId"]),
    }
    return summary, events


def build_open_position_plan(adapter, position, trades, strategies, by_symbol, contract_values, order_history):
    order_id = str(position.get("orderId", ""))
    inst_id = str(position.get("instId", ""))
    symbol = inst_id.split("-", 1)[0].upper()
    strategy_id = match_strategy(position, trades, strategies, by_symbol)
    if strategies[strategy_id]["symbol"] != symbol:
        raise RuntimeError("matched strategy symbol mismatch for open order " + order_id)

    total_quantity = dec(position.get("positions"))
    available_quantity = dec(position.get("availablePositions"), total_quantity)
    open_price = dec(position.get("averagePrice"))
    open_ms = timestamp_ms(position.get("createTime"))
    partial_pnl = dec(position.get("realizedPnl"))
    if total_quantity <= 0 or available_quantity < 0 or available_quantity > total_quantity or open_price <= 0 or not open_ms:
        raise RuntimeError("incomplete open position payload for order " + order_id)

    entry_order = order_history.get(order_id)
    if entry_order is None:
        raise RuntimeError("no exact order-history entry for open position " + order_id)
    if str(entry_order.get("instId", "")).upper() != inst_id.upper():
        raise RuntimeError("open order instrument mismatch for " + order_id)
    if abs(dec(entry_order.get("filledSize")) - total_quantity) > max(Decimal("0.000001"), total_quantity * Decimal("0.000001")):
        raise RuntimeError("open order quantity mismatch for " + order_id)
    if abs(dec(entry_order.get("averagePrice")) - open_price) > max(Decimal("0.00000001"), open_price * Decimal("0.00000001")):
        raise RuntimeError("open order price mismatch for " + order_id)

    detail = adapter._private_get(POSITION_DETAILS_PATH, {"orderId": order_id}).get("data", {})
    closes = detail.get("orderList", [])
    expected_closed_quantity = total_quantity - available_quantity
    close_qty = sum((dec(row.get("size")) for row in closes), ZERO)
    if abs(close_qty - expected_closed_quantity) > max(Decimal("0.000001"), total_quantity * Decimal("0.000001")):
        raise RuntimeError("partial close quantity mismatch for order %s: %.8f != %.8f" % (order_id, close_qty, expected_closed_quantity))

    contract_value = instrument_contract_value(adapter, inst_id, contract_values)
    position_id = strategy_id + ":" + symbol + ":blofin:" + order_id
    opened_iso = iso_from_ms(open_ms)
    open_side = str(position.get("positionSide", "")).lower()
    if open_side not in ("long", "short"):
        raise RuntimeError("invalid positionSide for open order " + order_id)
    side = str(entry_order.get("side", "")).lower()
    if side not in ("buy", "sell"):
        raise RuntimeError("invalid entry side for open order " + order_id)

    events = [{
        "timestamp": opened_iso,
        "strategy_id": strategy_id,
        "symbol": symbol,
        "position_id": position_id,
        "side": side,
        "quantity": float(total_quantity),
        "price": float(open_price),
        "value": float(total_quantity * open_price * contract_value),
        "trade_type": "perps",
        "details": "BloFin live position sync; orderId=" + order_id,
        "exchange_order_id": order_id,
        "exchange_fee": float(dec(entry_order.get("fee"))),
        "is_close": 0,
        "realized_pnl": 0.0,
        "pnl_gross": 1,
        "fee_source": "userfills",
    }]

    partial_realized_net = ZERO
    for close in closes:
        close_id = str(close.get("closeOrderId", ""))
        close_qty_leg = dec(close.get("size"))
        close_px = dec(close.get("averagePrice"))
        close_fee = dec(close.get("fee"))
        close_side = str(close.get("side", "")).lower()
        close_ms = timestamp_ms(close.get("orderTime"))
        if not close_id or close_qty_leg <= 0 or close_px <= 0 or not close_ms or close_side not in ("buy", "sell"):
            raise RuntimeError("incomplete partial close detail for order " + order_id)
        close_order = matching_order(order_history, inst_id, close_ms, close_side, close_qty_leg, close_px)
        history_fee = dec(close_order.get("fee"))
        if abs(history_fee - close_fee) > Decimal("0.000001"):
            raise RuntimeError("partial close fee mismatch for order %s/%s" % (order_id, close_order.get("orderId")))
        close_pnl = dec(close_order.get("pnl"))
        partial_realized_net += close_pnl - history_fee
        events.append({
            "timestamp": iso_from_ms(close_ms),
            "strategy_id": strategy_id,
            "symbol": symbol,
            "position_id": position_id,
            "side": close_side,
            "quantity": float(close_qty_leg),
            "price": float(close_px),
            "value": float(close_qty_leg * close_px * contract_value),
            "trade_type": "perps",
            "details": "BloFin partial close sync; positionOrderId=%s detailsCloseId=%s" % (order_id, close_id),
            "exchange_order_id": str(close_order["orderId"]),
            "exchange_fee": float(history_fee),
            "is_close": 1,
            "realized_pnl": float(close_pnl),
            "pnl_gross": 1,
            "fee_source": "userfills",
        })

    if closes and abs(partial_realized_net + sum((dec(row.get("fee")) for row in closes), ZERO) - partial_pnl) > Decimal("0.10"):
        raise RuntimeError("partial position PnL mismatch for open order " + order_id)
    position_state = {
        "strategy_id": strategy_id,
        "symbol": symbol,
        "position_id": position_id,
        "quantity": float(available_quantity),
        "initial_quantity": float(total_quantity),
        "avg_cost": float(open_price),
        "side": open_side,
        "multiplier": float(contract_value),
        "owner_strategy_id": strategy_id,
        "opened_at": opened_iso,
        "realized_pnl_accum": float(partial_realized_net),
    }
    summary = {
        "order_id": order_id,
        "strategy_id": strategy_id,
        "symbol": symbol,
        "total_quantity": float(total_quantity),
        "available_quantity": float(available_quantity),
        "partial_close_fills": len(closes),
        "partial_realized_pnl": float(partial_pnl),
    }
    return summary, events, position_state


def write_staged_database(source_path, output_path, strategy_ids, summaries, open_summaries, open_positions, events):
    if os.path.exists(output_path):
        raise FileExistsError("staging DB already exists: " + output_path)
    source = sqlite3.connect("file:" + os.path.abspath(source_path) + "?mode=ro", uri=True)
    target = sqlite3.connect(output_path)
    source.backup(target)
    source.close()
    try:
        target.execute("PRAGMA foreign_keys=ON")
        target.execute("BEGIN IMMEDIATE")
        ids = sorted(strategy_ids)
        placeholders = ",".join("?" for _ in ids)
        target.execute("DELETE FROM trades WHERE strategy_id IN (" + placeholders + ")", ids)
        target.execute("DELETE FROM closed_positions WHERE strategy_id IN (" + placeholders + ")", ids)
        target.execute("DELETE FROM positions WHERE strategy_id IN (" + placeholders + ")", ids)

        insert_trade = """INSERT INTO trades (
            strategy_id,timestamp,symbol,position_id,side,quantity,price,value,trade_type,details,
            exchange_order_id,exchange_fee,is_close,realized_pnl,pnl_gross,fee_source
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"""
        for event in sorted(events, key=lambda row: row["timestamp"]):
            target.execute(insert_trade, (
                event["strategy_id"], event["timestamp"], event["symbol"], event["position_id"],
                event["side"], event["quantity"], event["price"], event["value"], event["trade_type"],
                event["details"], event["exchange_order_id"], event["exchange_fee"], event["is_close"],
                event["realized_pnl"], event["pnl_gross"], event["fee_source"],
            ))

        insert_closed = """INSERT INTO closed_positions (
            strategy_id,symbol,quantity,avg_cost,side,multiplier,opened_at,closed_at,close_price,
            realized_pnl,close_reason,duration_seconds
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)"""
        for item in summaries:
            start = datetime.fromisoformat(item["opened_at"].replace("Z", "+00:00"))
            end = datetime.fromisoformat(item["closed_at"].replace("Z", "+00:00"))
            duration = max(0, int((end - start).total_seconds()))
            target.execute(insert_closed, (
                item["strategy_id"], item["symbol"], item["quantity"], item["open_price"],
                item["position_side"], item["contract_value"], item["opened_at"], item["closed_at"],
                item["close_price"], item["realized_pnl"], item["close_reason"], duration,
            ))

        insert_position = """INSERT INTO positions (
            strategy_id,symbol,position_id,quantity,initial_quantity,avg_cost,side,multiplier,
            owner_strategy_id,opened_at,realized_pnl_accum
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?)"""
        for item in open_positions:
            target.execute(insert_position, (
                item["strategy_id"], item["symbol"], item["position_id"], item["quantity"],
                item["initial_quantity"], item["avg_cost"], item["side"], item["multiplier"],
                item["owner_strategy_id"], item["opened_at"], item["realized_pnl_accum"],
            ))

        net_by_strategy = defaultdict(Decimal)
        for event in events:
            if event["pnl_gross"]:
                delta = dec(event["realized_pnl"]) - dec(event["exchange_fee"])
            elif event["is_close"]:
                delta = dec(event["realized_pnl"])
            else:
                delta = -dec(event["exchange_fee"])
            net_by_strategy[event["strategy_id"]] += delta
        cash_by_strategy = {}
        for strategy_id in ids:
            row = target.execute("SELECT initial_capital FROM strategies WHERE id=?", (strategy_id,)).fetchone()
            if row is None:
                raise RuntimeError("strategy row not found while rebuilding cash: " + strategy_id)
            cash = dec(row[0]) + net_by_strategy[strategy_id]
            target.execute("UPDATE strategies SET cash=? WHERE id=?", (float(cash), strategy_id))
            cash_by_strategy[strategy_id] = str(cash)

        target.commit()
        target.execute("PRAGMA wal_checkpoint(TRUNCATE)")
        target.execute("PRAGMA journal_mode=DELETE")
        target.close()
        check = sqlite3.connect("file:" + os.path.abspath(output_path) + "?mode=ro", uri=True)
        integrity = check.execute("PRAGMA integrity_check").fetchone()[0]
        ids = sorted(strategy_ids)
        placeholders = ",".join("?" for _ in ids)
        trades_after = check.execute("SELECT COUNT(*) FROM trades WHERE strategy_id IN (" + placeholders + ")", ids).fetchone()[0]
        closes_after = check.execute("SELECT COUNT(*) FROM trades WHERE is_close=1 AND strategy_id IN (" + placeholders + ")", ids).fetchone()[0]
        closed_after = check.execute("SELECT COUNT(*) FROM closed_positions WHERE strategy_id IN (" + placeholders + ")", ids).fetchone()[0]
        positions_after = check.execute("SELECT COUNT(*) FROM positions WHERE strategy_id IN (" + placeholders + ")", ids).fetchone()[0]
        check.close()
        expected_closes = sum(row["close_fills"] for row in summaries) + sum(row["partial_close_fills"] for row in open_summaries)
        if integrity != "ok" or trades_after != len(events) or closes_after != expected_closes or closed_after != len(summaries) or positions_after != len(open_positions):
            raise RuntimeError("staged DB verification failed: integrity=%s trades=%s closes=%s closed_positions=%s open_positions=%s" % (integrity, trades_after, closes_after, closed_after, positions_after))
        return {
            "integrity": integrity,
            "trades_after": trades_after,
            "close_fills_after": closes_after,
            "closed_positions_after": closed_after,
            "open_positions_after": positions_after,
            "cash_by_strategy": cash_by_strategy,
        }
    except Exception:
        try:
            target.rollback()
        except sqlite3.ProgrammingError:
            pass
        try:
            target.close()
        except sqlite3.ProgrammingError:
            pass
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--db", required=True, help="read-only source snapshot")
    parser.add_argument("--config", required=True, help="live config JSON")
    parser.add_argument("--since-ms", required=True, type=int, help="first timestamp of this live strategy cohort")
    parser.add_argument("--output-db", help="new staging database path; only written with --apply")
    parser.add_argument("--apply", action="store_true", help="write a new staging DB; source DB remains unchanged")
    args = parser.parse_args()
    if args.apply and not args.output_db:
        parser.error("--apply requires --output-db")

    from adapter import BloFinExchangeAdapter
    adapter = BloFinExchangeAdapter()
    if not adapter.is_live:
        raise RuntimeError("BloFin adapter is not in live mode")
    strategies, by_symbol = config_live_blofin_strategies(args.config)
    trades, old_positions, old_closed = load_source_rows(args.db, strategies.keys())
    active_symbols = set(by_symbol)
    exchange_open = [
        row for row in copy_open_positions_by_order(adapter)
        if dec(row.get("positions")) > 0 and timestamp_ms(row.get("createTime")) >= args.since_ms
    ]
    unknown_open = [
        row for row in exchange_open
        if str(row.get("instId", "")).split("-", 1)[0].upper() not in active_symbols
    ]
    if unknown_open:
        raise RuntimeError("unconfigured live copy positions present: " + ",".join(str(row.get("instId", "")) for row in unknown_open))
    for row in trades:
        if iso_to_ms(row.get("timestamp")) < args.since_ms:
            raise RuntimeError("live strategy trade predates --since-ms; refusing to delete older ledger")

    history = [row for row in copy_position_history(adapter) if timestamp_ms(row.get("createTime")) >= args.since_ms]
    open_ids = {str(row.get("orderId", "")) for row in exchange_open}
    history = [row for row in history if str(row.get("orderId", "")) not in open_ids]
    history.sort(key=lambda row: timestamp_ms(row.get("createTime")))
    if not history and not exchange_open:
        raise RuntimeError("no BloFin closed or open positions found for the selected live cohort")

    all_positions = history + exchange_open
    inst_ids = {str(row.get("instId", "")) for row in all_positions}
    order_history = copy_order_history(adapter, inst_ids, args.since_ms)
    contract_values = {}
    summaries = []
    open_summaries = []
    open_positions = []
    events = []
    by_strategy = defaultdict(Decimal)
    for position in history:
        summary, position_events = build_position_plan(adapter, position, trades, strategies, by_symbol, contract_values, order_history)
        summaries.append(summary)
        events.extend(position_events)
        by_strategy[summary["strategy_id"]] += dec(summary["realized_pnl"])
    for position in exchange_open:
        summary, position_events, position_state = build_open_position_plan(
            adapter, position, trades, strategies, by_symbol, contract_values, order_history
        )
        open_summaries.append(summary)
        open_positions.append(position_state)
        events.extend(position_events)
        by_strategy[summary["strategy_id"]] += dec(summary["partial_realized_pnl"])

    total_pnl = sum((dec(row["realized_pnl"]) for row in summaries), ZERO) + sum(
        (dec(row["partial_realized_pnl"]) for row in open_summaries), ZERO
    )
    net_by_strategy = defaultdict(Decimal)
    for event in events:
        if event["pnl_gross"]:
            delta = dec(event["realized_pnl"]) - dec(event["exchange_fee"])
        elif event["is_close"]:
            delta = dec(event["realized_pnl"])
        else:
            delta = -dec(event["exchange_fee"])
        net_by_strategy[event["strategy_id"]] += delta
    result = {
        "source_trade_rows": len(trades),
        "source_open_positions": len(old_positions),
        "source_closed_positions": len(old_closed),
        "exchange_closed_positions": len(summaries),
        "exchange_open_positions": len(open_positions),
        "exchange_close_fills": sum(row["close_fills"] for row in summaries),
        "exchange_partial_close_fills": sum(row["partial_close_fills"] for row in open_summaries),
        "reconstructed_trade_rows": len(events),
        "pnl_total": str(total_pnl),
        "pnl_by_strategy": {key: str(value) for key, value in sorted(by_strategy.items())},
        "net_pnl_by_strategy": {key: str(value) for key, value in sorted(net_by_strategy.items())},
        "positions": summaries,
        "open_positions": open_summaries,
    }
    if args.apply:
        result["stage_verification"] = write_staged_database(
            args.db, args.output_db, strategies.keys(), summaries, open_summaries, open_positions, events
        )
        result["output_db"] = args.output_db
    else:
        result["dry_run"] = True
    print(json.dumps(result, separators=(",", ":")))


if __name__ == "__main__":
    main()
