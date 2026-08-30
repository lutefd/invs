from __future__ import annotations

import hashlib
import json
from copy import deepcopy
from decimal import Decimal
from pathlib import Path

import pytest

from research.experiments import build_experiment_spec, canonical_json

SECURITY_A = "10000000-0000-4000-8000-000000000001"
SECURITY_B = "10000000-0000-4000-8000-000000000002"
UNIVERSE_ID = "20000000-0000-4000-8000-000000000001"
AVAILABLE_AT = "2025-01-01T00:01:00Z"
OBSERVED_AT = "2025-01-01T00:00:00Z"


def _artifact_id(ordinal: int) -> str:
    return f"30000000-0000-4000-8000-{ordinal:012d}"


def _write_artifact(root: Path, kind: str, ordinal: int, rows: list[dict[str, object]]) -> dict[str, object]:
    document = {
        "schema_version": "1.0.0",
        "artifact_kind": kind,
        "artifact_id": _artifact_id(ordinal),
        "available_at": AVAILABLE_AT,
        "rows": rows,
    }
    path = root / "inputs" / f"{kind}.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    content = canonical_json(document) + b"\n"
    path.write_bytes(content)
    return {
        "kind": kind,
        "artifact_id": document["artifact_id"],
        "path": f"inputs/{kind}.json",
        "sha256": hashlib.sha256(content).hexdigest(),
        "available_at": AVAILABLE_AT,
        "historical_fitness": "backtest_safe",
    }


def _price(security_id: str, session_date: str, open_price: str, close: str) -> dict[str, object]:
    high = max(open_price, close, key=Decimal)
    low = min(open_price, close, key=Decimal)
    return {
        "security_id": security_id,
        "session_date": session_date,
        "observed_at": OBSERVED_AT,
        "available_at": AVAILABLE_AT,
        "currency": "USD",
        "price_basis": "raw",
        "open": open_price,
        "high": high,
        "low": low,
        "close": close,
        "volume": "100000",
        "has_volume": True,
    }


def _base_fixture(tmp_path: Path, *, strategy_name: str = "equal_weight") -> tuple[dict, Path]:
    dates = ("2025-01-02", "2025-01-03", "2025-01-06")
    calendar = [
        {
            "session_date": session_date,
            "open_at": f"{session_date}T14:30:00Z",
            "close_at": f"{session_date}T21:00:00Z",
            "available_at": AVAILABLE_AT,
        }
        for session_date in dates
    ]
    prices = [
        _price(SECURITY_A, "2025-01-02", "10", "10"),
        _price(SECURITY_B, "2025-01-02", "20", "20"),
        _price(SECURITY_A, "2025-01-03", "10", "12"),
        _price(SECURITY_B, "2025-01-03", "20", "22"),
        _price(SECURITY_A, "2025-01-06", "12", "13"),
        _price(SECURITY_B, "2025-01-06", "22", "21"),
    ]
    membership = [
        {
            "security_id": security_id,
            "valid_from": "2025-01-01",
            "valid_until": None,
            "member": True,
            "available_at": AVAILABLE_AT,
            "revision": 0,
        }
        for security_id in (SECURITY_A, SECURITY_B)
    ]
    refs = [
        _write_artifact(tmp_path, "prices", 1, prices),
        _write_artifact(tmp_path, "calendar", 2, calendar),
        _write_artifact(tmp_path, "membership", 3, membership),
    ]
    parameters = {} if strategy_name == "buy_and_hold" else {"rebalance_frequency": "monthly"}
    spec = build_experiment_spec(
        {
            "schema_version": "1.0.0",
            "strategy": {
                "name": strategy_name,
                "version": "1.0.0",
                "git_commit": "unknown",
                "parameters": parameters,
            },
            "period": {"start_date": dates[0], "end_date": dates[-1]},
            "universe": {
                "universe_id": UNIVERSE_ID,
                "version": "1.0.0",
                "security_ids": [SECURITY_B, SECURITY_A],
                "membership_fingerprint": "a" * 64,
            },
            "inputs": refs,
            "benchmark": {"security_id": SECURITY_A, "currency": "USD"},
            "decision_policy": {
                "name": "after_close_next_session_open",
                "frequency": "daily",
                "signal_delay_sessions": 1,
                "execution_price": "open",
            },
            "accounting_policy": {
                "base_currency": "USD",
                "reporting_currency": None,
                "initial_cash": "10000",
                "fractional_shares": True,
                "rebalance_frequency": "monthly",
            },
            "cost_policy": {
                "version": "1.0.0",
                "commission_bps": "0",
                "fixed_fee": "0",
                "minimum_fee": "0",
                "spread_bps": "0",
                "slippage_bps": "0",
                "tax_bps": "0",
            },
            "risk_policy": {
                "max_gross_exposure": "1",
                "max_position_weight": "1",
                "max_participation": "1",
            },
            "partitions": [
                {"name": "development", "kind": "development", "start_date": dates[0], "end_date": dates[0]},
                {"name": "validation", "kind": "validation", "start_date": dates[1], "end_date": dates[1]},
                {"name": "holdout", "kind": "holdout", "start_date": dates[2], "end_date": dates[2]},
            ],
            "missing_data_policy": "reject_trade",
        }
    )
    return spec, tmp_path


def _rewrite_document(path: Path, document: dict[str, object]) -> str:
    content = canonical_json(document) + b"\n"
    path.write_bytes(content)
    return hashlib.sha256(content).hexdigest()


def _extended_action_fixture(tmp_path: Path) -> tuple[dict, Path]:
    spec, root = _base_fixture(tmp_path)
    calendar_path = root / "inputs" / "calendar.json"
    calendar_document = json.loads(calendar_path.read_text(encoding="utf-8"))
    calendar_document["rows"].append(
        {
            "session_date": "2025-01-07",
            "open_at": "2025-01-07T14:30:00Z",
            "close_at": "2025-01-07T21:00:00Z",
            "available_at": AVAILABLE_AT,
        }
    )
    calendar_sha256 = _rewrite_document(calendar_path, calendar_document)

    prices_path = root / "inputs" / "prices.json"
    prices_document = json.loads(prices_path.read_text(encoding="utf-8"))
    for row in prices_document["rows"]:
        if row["security_id"] == SECURITY_A and row["session_date"] == "2025-01-06":
            row.update({"open": "6", "high": "6", "low": "6", "close": "6"})
    prices_document["rows"].extend(
        [
            _price(SECURITY_A, "2025-01-07", "6", "7"),
            _price(SECURITY_B, "2025-01-07", "20", "20"),
        ]
    )
    prices_sha256 = _rewrite_document(prices_path, prices_document)

    membership_path = root / "inputs" / "membership.json"
    membership_document = json.loads(membership_path.read_text(encoding="utf-8"))
    membership_document["rows"][1]["valid_until"] = "2025-01-06"
    membership_document["rows"].append(
        {
            "security_id": SECURITY_B,
            "valid_from": "2025-01-06",
            "valid_until": None,
            "member": False,
            "available_at": AVAILABLE_AT,
            "revision": 0,
        }
    )
    membership_sha256 = _rewrite_document(membership_path, membership_document)

    actions = [
        {
            "id": _artifact_id(11),
            "security_id": SECURITY_A,
            "action_type": "split",
            "effective_date": "2025-01-06",
            "payment_date": None,
            "available_at": AVAILABLE_AT,
            "currency": "USD",
            "ratio_numerator": "2",
            "ratio_denominator": "1",
            "cash_amount": "0",
            "settlement_price": None,
            "target_security_id": None,
        },
        {
            "id": _artifact_id(12),
            "security_id": SECURITY_A,
            "action_type": "cash_dividend",
            "effective_date": "2025-01-05",
            "payment_date": "2025-01-06",
            "available_at": AVAILABLE_AT,
            "currency": "USD",
            "ratio_numerator": "1",
            "ratio_denominator": "1",
            "cash_amount": "0.5",
            "settlement_price": None,
            "target_security_id": None,
        },
        {
            "id": _artifact_id(13),
            "security_id": SECURITY_B,
            "action_type": "delisting",
            "effective_date": "2025-01-07",
            "payment_date": None,
            "available_at": AVAILABLE_AT,
            "currency": "USD",
            "ratio_numerator": "1",
            "ratio_denominator": "1",
            "cash_amount": "0",
            "settlement_price": "20",
            "target_security_id": None,
        },
    ]
    action_ref = _write_artifact(root, "corporate_actions", 4, actions)

    changed = deepcopy(spec)
    changed.pop("experiment_id")
    changed["period"]["end_date"] = "2025-01-07"
    changed["partitions"] = [
        {"name": "development", "kind": "development", "start_date": "2025-01-02", "end_date": "2025-01-02"},
        {"name": "validation", "kind": "validation", "start_date": "2025-01-03", "end_date": "2025-01-05"},
        {"name": "holdout", "kind": "holdout", "start_date": "2025-01-06", "end_date": "2025-01-07"},
    ]
    refs = {reference["kind"]: reference for reference in changed["inputs"]}
    refs["calendar"]["sha256"] = calendar_sha256
    refs["prices"]["sha256"] = prices_sha256
    refs["membership"]["sha256"] = membership_sha256
    changed["inputs"].append(action_ref)
    return build_experiment_spec(changed), root


def _foreign_currency_fixture(tmp_path: Path) -> tuple[dict, Path]:
    spec, root = _base_fixture(tmp_path)
    prices_path = root / "inputs" / "prices.json"
    prices_document = json.loads(prices_path.read_text(encoding="utf-8"))
    for row in prices_document["rows"]:
        row["currency"] = "BRL"
    prices_sha256 = _rewrite_document(prices_path, prices_document)
    fx_ref = _write_artifact(
        root,
        "fx",
        4,
        [
            {
                "fixing_at": "2025-01-01T00:00:00Z",
                "base_currency": "USD",
                "quote_currency": "BRL",
                "rate": "5",
                "available_at": AVAILABLE_AT,
            }
        ],
    )
    changed = deepcopy(spec)
    changed.pop("experiment_id")
    changed["benchmark"]["currency"] = "BRL"
    changed["accounting_policy"]["base_currency"] = "USD"
    changed["accounting_policy"]["reporting_currency"] = "BRL"
    price_ref = next(reference for reference in changed["inputs"] if reference["kind"] == "prices")
    price_ref["sha256"] = prices_sha256
    changed["inputs"].append(fx_ref)
    return build_experiment_spec(changed), root


def test_daily_engine_delays_orders_to_next_eligible_session_and_balances(tmp_path: Path) -> None:
    from research.backtest import simulate_backtest

    spec, root = _base_fixture(tmp_path)

    run = simulate_backtest(spec, data_root=root)

    assert len(run.nav) == 3
    assert {order["execution_session"] for order in run.orders} == {"2025-01-03"}
    assert len(run.orders) == len(run.fills) == 2
    assert all(order["status"] == "filled" for order in run.orders)
    assert run.nav[0]["nav"] == "10000"
    assert run.nav[1]["nav"] == "11500"
    assert run.nav[2]["nav"] == "11750"
    for row in run.nav:
        assert row["nav"] == str(int(row["cash_base"]) + int(row["positions_value_base"]))
    assert run.metrics["values"]["total_return"] == "0.175"


def test_clean_replay_is_byte_deterministic(tmp_path: Path) -> None:
    from research.backtest import simulate_backtest

    spec, root = _base_fixture(tmp_path / "first")
    replay_spec, replay_root = _base_fixture(tmp_path / "second")

    first = simulate_backtest(spec, data_root=root)
    second = simulate_backtest(replay_spec, data_root=replay_root)

    assert first == second


def test_tampered_pinned_input_is_rejected(tmp_path: Path) -> None:
    from research.backtest import simulate_backtest
    from research.backtest_inputs import BacktestInputError

    spec, root = _base_fixture(tmp_path)
    prices = root / "inputs" / "prices.json"
    document = json.loads(prices.read_text(encoding="utf-8"))
    document["rows"][0]["close"] = "999"
    prices.write_bytes(canonical_json(document) + b"\n")

    with pytest.raises(BacktestInputError, match="hash mismatch"):
        simulate_backtest(spec, data_root=root)


def test_adjusted_prices_are_rejected_before_simulation(tmp_path: Path) -> None:
    from research.backtest_inputs import BacktestInputError, load_backtest_inputs

    spec, root = _base_fixture(tmp_path)
    prices = root / "inputs" / "prices.json"
    document = json.loads(prices.read_text(encoding="utf-8"))
    document["rows"][0]["price_basis"] = "split_adjusted"
    content = canonical_json(document) + b"\n"
    prices.write_bytes(content)
    changed = deepcopy(spec)
    price_ref = next(item for item in changed["inputs"] if item["kind"] == "prices")
    price_ref["sha256"] = hashlib.sha256(content).hexdigest()
    changed.pop("experiment_id")
    changed = build_experiment_spec(changed)

    with pytest.raises(BacktestInputError, match="price_basis must be raw"):
        load_backtest_inputs(changed, data_root=root)


def test_future_execution_price_can_halt_instead_of_looking_ahead(tmp_path: Path) -> None:
    from research.backtest import BacktestMissingDataError, simulate_backtest

    spec, root = _base_fixture(tmp_path)
    prices = root / "inputs" / "prices.json"
    document = json.loads(prices.read_text(encoding="utf-8"))
    document["rows"][2]["available_at"] = "2025-01-03T15:00:00Z"
    document["available_at"] = "2025-01-03T15:00:00Z"
    content = canonical_json(document) + b"\n"
    prices.write_bytes(content)
    changed = deepcopy(spec)
    price_ref = next(item for item in changed["inputs"] if item["kind"] == "prices")
    price_ref["sha256"] = hashlib.sha256(content).hexdigest()
    price_ref["available_at"] = document["available_at"]
    changed["missing_data_policy"] = "halt_experiment"
    changed.pop("experiment_id")
    changed = build_experiment_spec(changed)

    with pytest.raises(BacktestMissingDataError, match="missing executable open"):
        simulate_backtest(changed, data_root=root)


def test_actions_and_membership_removal_are_accounted_before_next_open(tmp_path: Path) -> None:
    from research.backtest import simulate_backtest

    spec, root = _extended_action_fixture(tmp_path)

    run = simulate_backtest(spec, data_root=root)

    assert run.nav[-2]["nav"] == "11750"
    assert run.nav[-1]["nav"] == "12500"
    assert run.holdings[-1]["security_id"] == SECURITY_A
    assert run.holdings[-1]["quantity"] == "1000"
    assert any(row["event_type"] == "split" for row in run.ledger)
    assert any(row["event_type"] == "dividend" and row["amount_local"] == "500" for row in run.ledger)
    assert any(row["event_type"] == "delisting" and row["amount_local"] == "5000" for row in run.ledger)
    removal_orders = [order for order in run.orders if order["reason"] == "membership_exit"]
    assert len(removal_orders) == 1
    assert removal_orders[0]["execution_session"] == "2025-01-07"
    assert removal_orders[0]["status"] == "rejected"


def test_conservative_costs_reduce_fills_and_publish_cost_attribution(tmp_path: Path) -> None:
    from research.backtest import simulate_backtest

    zero_spec, zero_root = _base_fixture(tmp_path / "zero")
    cost_spec = deepcopy(zero_spec)
    cost_spec.pop("experiment_id")
    cost_spec["cost_policy"] = {
        "version": "1.0.0",
        "commission_bps": "10",
        "fixed_fee": "1",
        "minimum_fee": "2",
        "spread_bps": "20",
        "slippage_bps": "10",
        "tax_bps": "5",
    }
    cost_spec = build_experiment_spec(cost_spec)

    zero_run = simulate_backtest(zero_spec, data_root=zero_root)
    cost_run = simulate_backtest(cost_spec, data_root=zero_root)

    assert len(cost_run.fills) == 2
    assert all(Decimal(fill["fee"]) > 0 for fill in cost_run.fills)
    assert all(Decimal(fill["spread_cost"]) > 0 for fill in cost_run.fills)
    assert all(Decimal(fill["slippage_cost"]) > 0 for fill in cost_run.fills)
    assert all(Decimal(fill["tax"]) > 0 for fill in cost_run.fills)
    assert Decimal(cost_run.metrics["values"]["total_cost"]) > 0
    assert Decimal(cost_run.nav[-1]["nav"]) < Decimal(zero_run.nav[-1]["nav"])


def test_foreign_currency_funding_and_reporting_use_pinned_fx(tmp_path: Path) -> None:
    from research.backtest import simulate_backtest

    spec, root = _foreign_currency_fixture(tmp_path)

    run = simulate_backtest(spec, data_root=root)

    assert len(run.fills) == 2
    assert run.nav[0]["reporting_nav"] == "50000"
    assert run.nav[-1]["reporting_nav"] == "58750"
    assert sum(row["event_type"] == "fx_conversion" for row in run.ledger) == 4
    assert all(row["currency"] == "BRL" for row in run.fills)


def test_baseline_signals_are_deterministic_and_future_bounded() -> None:
    from research.strategies import baseline_targets, equal_weight_targets

    current_history = {
        SECURITY_A: tuple(Decimal(value) for value in ("10", "11", "12", "13")),
        SECURITY_B: tuple(Decimal(value) for value in ("20", "21", "22", "21")),
    }
    future_history = {
        SECURITY_A: (*current_history[SECURITY_A], Decimal(999999)),
        SECURITY_B: (*current_history[SECURITY_B], Decimal(1)),
    }
    parameters = {
        "lookback_sessions": 2,
        "skip_sessions": 1,
        "top_k": 1,
        "rebalance_frequency": "daily",
    }

    equal = equal_weight_targets([SECURITY_B, SECURITY_A])
    momentum = baseline_targets(
        "momentum_12_1",
        security_ids=[SECURITY_A, SECURITY_B],
        price_history=current_history,
        session_index=3,
        parameters=parameters,
    )
    future_momentum = baseline_targets(
        "momentum_12_1",
        security_ids=[SECURITY_A, SECURITY_B],
        price_history=future_history,
        session_index=3,
        parameters=parameters,
    )

    assert equal == {SECURITY_A: Decimal("0.5"), SECURITY_B: Decimal("0.5")}
    assert momentum == {SECURITY_A: Decimal(1)}
    assert future_momentum == momentum
