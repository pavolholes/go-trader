import os
import sys
import importlib.util
import json

import pytest


ADAPTER_PATH = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "platforms", "blofin"))
sys.path.insert(0, ADAPTER_PATH)
from adapter import BloFinExchangeAdapter


def adapter_with_history(pages):
    adapter = object.__new__(BloFinExchangeAdapter)
    calls = []

    def private_get(path, params, timeout=15):
        calls.append((path, dict(params)))
        assert path == "/api/v1/copytrading/trade/orders-history"
        if params.get("before"):
            return {"data": pages[1]}
        return {"data": pages[0]}

    adapter._private_get = private_get
    return adapter, calls


def test_copy_order_fill_selects_matching_order_not_first_recent_fill():
    adapter, calls = adapter_with_history([[
        {"orderId": "unrelated", "filledSize": "17.9", "averagePrice": "0.22264", "fee": "0.23911536"},
        {"orderId": "wanted", "filledSize": "8.33", "averagePrice": "0.09244", "fee": "0.46201512"},
    ]])

    fill = adapter.get_copy_order_fill("wanted", "DOGE-USDT", tries=1)

    assert fill == {"avg_px": 0.09244, "total_sz": 8.33, "fee": 0.46201512, "oid": "wanted"}
    assert calls == [
        ("/api/v1/copytrading/trade/orders-history", {"instId": "DOGE-USDT", "limit": "20"})
    ]


def test_copy_order_fill_retries_after_history_lags_order_ack(monkeypatch):
    adapter = object.__new__(BloFinExchangeAdapter)
    responses = iter([
        {"data": []},
        {"data": [{"orderId": "wanted", "filledSize": "250", "averagePrice": "150.25", "fee": "0.225375"}]},
    ])
    timeouts = []
    sleeps = []

    def private_get(path, params, timeout=15):
        assert path == "/api/v1/copytrading/trade/orders-history"
        assert params == {"instId": "SPCX-USDT", "limit": "20"}
        timeouts.append(timeout)
        return next(responses)

    adapter._private_get = private_get
    monkeypatch.setattr("adapter.time.sleep", lambda delay: sleeps.append(delay))

    fill = adapter.get_copy_order_fill(
        "wanted", "SPCX-USDT", tries=3, max_pages=1,
        request_timeout=4, retry_delay=1,
    )

    assert fill == {"avg_px": 150.25, "total_sz": 250.0, "fee": 0.225375, "oid": "wanted"}
    assert timeouts == [4, 4]
    assert sleeps == [1]


def test_copy_order_fill_paginates_by_symbol_to_find_old_order():
    first_page = [
        {"orderId": "new-%d" % i, "filledSize": "1", "averagePrice": "10", "fee": "0.01"}
        for i in range(20)
    ]
    second_page = [{"orderId": "wanted", "filledSize": "2", "averagePrice": "12", "fee": "0.02"}]
    adapter, calls = adapter_with_history([first_page, second_page])

    fill = adapter.get_copy_order_fill("wanted", "ADA-USDT", tries=1)

    assert fill == {"avg_px": 12.0, "total_sz": 2.0, "fee": 0.02, "oid": "wanted"}
    assert len(calls) == 2
    assert calls[1][1] == {"instId": "ADA-USDT", "limit": "20", "before": "new-19"}


def test_bounded_execution_poll_can_stop_after_recent_page():
    first_page = [
        {"orderId": "recent-%d" % i, "filledSize": "1", "averagePrice": "10", "fee": "0.01"}
        for i in range(20)
    ]
    adapter, calls = adapter_with_history([first_page, [{"orderId": "wanted", "filledSize": "2", "averagePrice": "12", "fee": "0.02"}]])

    fill = adapter.get_copy_order_fill("wanted", "ADA-USDT", tries=1, max_pages=1)

    assert fill == {}
    assert len(calls) == 1


def test_copy_order_fill_never_returns_a_different_order():
    adapter, _ = adapter_with_history([[
        {"orderId": "unrelated", "filledSize": "17.9", "averagePrice": "0.22264", "fee": "0.23911536"}
    ]])

    fill = adapter.get_copy_order_fill("wanted", "XLM-USDT", tries=1)

    assert fill == {}


def test_copy_order_fill_matches_client_id_when_close_detail_id_differs():
    client_order_id = "c0ffee1234567890abcdef1234567890"
    adapter, _ = adapter_with_history([[
        {
            "orderId": "16967850",
            "clientOrderId": client_order_id,
            "filledSize": "201",
            "averagePrice": "148.83",
            "fee": "0.17948898",
        }
    ]])

    fill = adapter.get_copy_order_fill(
        "7363438", "SPCX-USDT", tries=1, client_order_id=client_order_id
    )

    assert fill == {
        "avg_px": 148.83,
        "total_sz": 201.0,
        "fee": 0.17948898,
        "oid": "16967850",
    }


def copy_adapter(current_positions=None):
    adapter = object.__new__(BloFinExchangeAdapter)
    adapter._is_live = True
    adapter.trade_account = "copy"
    adapter._lot_size_cache = {"XLM-USDT": (0.1, 100.0)}
    adapter.get_copy_positions = lambda inst_id="": current_positions or []
    adapter.quantize_size = lambda inst_id, size, size_in_contracts=False: str(size)
    submitted = {}
    leverage_calls = []

    def set_leverage(inst_id, leverage, margin_mode="cross", pos_side=""):
        leverage_calls.append((inst_id, leverage, margin_mode, pos_side))

    def place_order(**kwargs):
        submitted.update(kwargs)
        return {"data": []}

    adapter.place_order = place_order
    adapter.set_leverage = set_leverage
    return adapter, submitted, leverage_calls


@pytest.mark.parametrize(("is_buy", "expected_side"), [(True, "long"), (False, "short")])
def test_flat_copy_account_open_selects_side_from_order(is_buy, expected_side):
    adapter, submitted, leverage_calls = copy_adapter()

    adapter.market_open("XLM", is_buy, 1.0)

    assert submitted["pos_side"] == expected_side
    assert leverage_calls == []


def test_copy_open_applies_configured_leverage_to_selected_side():
    adapter, submitted, leverage_calls = copy_adapter()

    adapter.market_open("XLM", True, 1.0, leverage=75)

    assert submitted["pos_side"] == "long"
    assert leverage_calls == [("XLM-USDT", "75", "cross", "long")]


def test_copy_order_forwards_stable_client_order_id():
    adapter, submitted, _ = copy_adapter()

    adapter.market_open("XLM", True, 1.0, client_order_id="client-order-123")

    assert submitted["client_oid"] == "client-order-123"


def test_copy_account_refuses_unmarked_open_that_would_flip_tracked_position():
    adapter, submitted, leverage_calls = copy_adapter()

    with pytest.raises(RuntimeError, match="DB tracks long"):
        adapter.market_open("XLM", False, 1.0, pos_side_hint="long")

    assert submitted == {}
    assert leverage_calls == []


def test_execute_parser_forwards_close_flag_and_configured_leverage(monkeypatch):
    path = os.path.join(os.path.dirname(__file__), "check_blofin.py")
    spec = importlib.util.spec_from_file_location("check_blofin_parser_test", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    got = []
    monkeypatch.setattr(module, "run_execute", lambda *args: got.append(args))
    monkeypatch.setattr(sys, "argv", [
        "check_blofin.py", "--execute", "--symbol=SPCX", "--side=sell", "--size=504",
        "--mode=live", "--pos-side-hint=long", "--is-close", "--leverage=75",
    ])

    module.main()

    assert got == [("SPCX", "sell", 504.0, "live", False, "long", True, 75.0)]


def test_execute_handles_object_shaped_copy_order_response(monkeypatch, capsys):
    import adapter as adapter_module

    path = os.path.join(os.path.dirname(__file__), "check_blofin.py")
    spec = importlib.util.spec_from_file_location("check_blofin_object_response_test", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    calls = []
    client_ids = []

    class FakeAdapter:
        trade_account = "copy"

        def market_open(self, *args, **kwargs):
            client_ids.append(kwargs["client_order_id"])
            # The place-order acknowledgement returns a close-detail ID, not
            # the parent Copy order ID used by orders-history.
            return {"code": "0", "data": {"orderId": "7361243"}, "contract_value": 0.01}

        def get_copy_order_fill(self, order_id, inst_id, tries=10, client_order_id="", max_pages=5,
                                request_timeout=15, retry_delay=3):
            calls.append((order_id, inst_id, client_order_id, tries, max_pages, request_timeout, retry_delay))
            return {
                "avg_px": 2688.85,
                "total_sz": 11.1,
                "fee": 0.17907741,
                "oid": "16949003",
            }

    monkeypatch.setattr(adapter_module, "BloFinExchangeAdapter", FakeAdapter)

    module.run_execute("ETH", "sell", 11.16, "live", False, "long", True, 75)

    output = json.loads(capsys.readouterr().out)
    assert len(client_ids) == 1 and len(client_ids[0]) == 32
    int(client_ids[0], 16)
    assert calls == [("7361243", "ETH-USDT", client_ids[0], 3, 1, 4, 1)]
    assert output["execution"]["fill"] == {
        "avg_px": 2688.85,
        "total_sz": 11.1,
        "fee": 0.17907741,
        "oid": "16949003",
        "contract_value": 0.01,
        "client_order_id": client_ids[0],
    }


def test_copy_order_ack_without_history_fill_does_not_assume_requested_size(monkeypatch, capsys):
    import adapter as adapter_module

    path = os.path.join(os.path.dirname(__file__), "check_blofin.py")
    spec = importlib.util.spec_from_file_location("check_blofin_unfilled_ack_test", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    client_ids = []

    class FakeAdapter:
        trade_account = "copy"

        def market_open(self, *args, **kwargs):
            client_ids.append(kwargs["client_order_id"])
            return {"code": "0", "data": {"orderId": "unfilled"}, "contract_value": 0.01}

        def get_copy_order_fill(self, order_id, inst_id, tries=10, client_order_id="", max_pages=5,
                                request_timeout=15, retry_delay=3):
            return {}

    monkeypatch.setattr(adapter_module, "BloFinExchangeAdapter", FakeAdapter)

    module.run_execute("ETH", "sell", 11.16, "live", False, "long", True, 75)

    output = json.loads(capsys.readouterr().out)
    assert output["execution"]["fill"]["avg_px"] == 0
    assert output["execution"]["fill"]["total_sz"] == 0
    assert output["execution"]["fill"]["oid"] == "unfilled"
    assert output["execution"]["fill"]["client_order_id"] == client_ids[0]
