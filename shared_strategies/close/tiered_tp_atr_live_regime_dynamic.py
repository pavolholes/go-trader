
from __future__ import annotations

from copy import copy

from tiered_tp_atr_live_regime import evaluate_live_regime_tiers


def evaluate(position: dict, market: dict, params: dict) -> dict:
    # Go owns and persists the regime debounce. On the confirming check only,
    # preview the label the scheduler will apply after this decision is read.
    position = position or {}
    params = params or {}
    applied = str(
        position.get("regime_applied_label")
        or position.get("regime", "")
        or ""
    ).strip()
    current = str((market or {}).get("regime", "") or "").strip()
    pending = str(position.get("regime_pending_label", "") or "").strip()
    try:
        confirm_cycles = int(params.get("regime_confirm_cycles", 2) or 2)
        pending_count = int(position.get("regime_pending_count", 0) or 0)
    except (TypeError, ValueError):
        confirm_cycles, pending_count = 2, 0
    if confirm_cycles < 1:
        confirm_cycles = 2
    apply_after_this_check = (
        current
        and current != applied
        and (
            confirm_cycles == 1
            or (current == pending and pending_count + 1 >= confirm_cycles)
        )
    )
    effective_position = copy(position)
    if apply_after_this_check:
        effective_position["regime_applied_label"] = current
    return evaluate_live_regime_tiers(
        effective_position,
        market,
        params,
        "tiered_tp_atr_live_regime_dynamic",
        regime_source="applied",
    )
