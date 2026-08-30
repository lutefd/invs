from __future__ import annotations

import hashlib
import json
import shutil
from copy import deepcopy
from pathlib import Path

import pytest

from research.experiments import canonical_json

SECURITY_A = "10000000-0000-4000-8000-000000000001"
SECURITY_B = "10000000-0000-4000-8000-000000000002"
UNIVERSE_ID = "20000000-0000-4000-8000-000000000001"
ACCOUNT_ID = "40000000-0000-4000-8000-000000000001"
AVAILABLE_AT = "2025-01-01T00:01:00Z"


def _write_artifact(
    root: Path,
    kind: str,
    ordinal: int,
    rows: list[dict[str, object]],
    *,
    available_at: str = AVAILABLE_AT,
) -> dict[str, object]:
    document = {
        "schema_version": "1.0.0",
        "artifact_kind": kind,
        "artifact_id": f"30000000-0000-4000-8000-{ordinal:012d}",
        "available_at": available_at,
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
        "available_at": available_at,
        "fitness": "current_research_only",
    }


def _price(security_id: str, session_date: str, open_price: str, close: str) -> dict[str, object]:
    high = max(open_price, close, key=lambda value: float(value))
    low = min(open_price, close, key=lambda value: float(value))
    return {
        "security_id": security_id,
        "session_date": session_date,
        "observed_at": "2025-01-01T00:00:00Z",
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


def _account_fixture(
    tmp_path: Path,
    *,
    dates: tuple[str, ...] = ("2025-01-02", "2025-01-03", "2025-01-06", "2025-01-07"),
    strategy_name: str = "equal_weight",
    risk_overrides: dict[str, object] | None = None,
) -> tuple[dict[str, object], Path]:
    prices: list[dict[str, object]] = []
    for index, session_date in enumerate(dates):
        prices.extend(
            [
                _price(SECURITY_A, session_date, str(10 + max(index - 1, 0)), str(10 + index)),
                _price(SECURITY_B, session_date, str(20 + max(index - 1, 0)), str(20 + index)),
            ]
        )
    calendar = [
        {
            "session_date": session_date,
            "open_at": f"{session_date}T14:30:00Z",
            "close_at": f"{session_date}T21:00:00Z",
            "available_at": AVAILABLE_AT,
        }
        for session_date in dates
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
    parameters = {} if strategy_name == "buy_and_hold" else {"rebalance_frequency": "daily"}
    risk = {
        "version": "1.0.0",
        "max_gross_exposure": "1",
        "max_position_weight": "1",
        "max_sector_exposure": "1",
        "max_country_exposure": "1",
        "max_currency_exposure": "1",
        "max_theme_exposure": "1",
        "minimum_cash_reserve": "0",
        "max_turnover": "1",
        "max_daily_notional": "100000",
        "max_price_gap": "1",
        "max_stale_sessions": 10,
        "max_participation": "1",
        "max_drawdown": "1",
        "prohibited_security_ids": [],
    }
    if risk_overrides:
        risk.update(risk_overrides)
    return (
        {
            "schema_version": "1.0.0",
            "account_id": ACCOUNT_ID,
            "name": "v0.6 test account",
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
            "security_metadata": [
                {"security_id": SECURITY_A, "country": "US", "sector": "technology", "themes": ["ai"]},
                {"security_id": SECURITY_B, "country": "US", "sector": "financials", "themes": ["banking"]},
            ],
            "inputs": refs,
            "benchmark": {"security_id": SECURITY_A, "currency": "USD"},
            "decision_policy": {
                "frequency": "daily",
                "decision_at": "close",
                "signal_delay_sessions": 1,
                "execution_price": "open",
                "stale_after_sessions": 10,
                "max_price_gap": "1",
            },
            "accounting_policy": {
                "base_currency": "USD",
                "initial_cash": "10000",
                "fractional_shares": True,
                "rebalance_frequency": "daily",
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
            "risk_policy": risk,
            "approval_policy": {"mode": "manual"},
            "missing_data_policy": "halt_decision",
        },
        tmp_path,
    )


def test_paper_account_requires_explicit_forward_fitness(tmp_path: Path) -> None:
    from research.paper import PaperSpecError, validate_paper_account

    account, _ = _account_fixture(tmp_path)
    normalized = validate_paper_account(account)
    assert normalized["inputs"][0]["fitness"] == "current_research_only"

    invalid = deepcopy(account)
    invalid["inputs"][0]["fitness"] = "installation_replay_only"
    with pytest.raises(PaperSpecError, match="not admitted"):
        validate_paper_account(invalid)


def test_paper_proposal_approval_fill_and_rebuild_are_idempotent(tmp_path: Path) -> None:
    from research.paper import (
        LedgerStore,
        approve_paper_decision,
        create_paper_account,
        rebuild_paper_account,
        run_paper_session,
    )

    account, root = _account_fixture(tmp_path)
    ledger_root = root / "ledger"
    created = create_paper_account(account, ledger_root=ledger_root)
    assert created["event_count"] == 1

    first = run_paper_session(account, data_root=root, ledger_root=ledger_root, session_date="2025-01-02")
    assert first["decision_status"] == "proposed"
    assert first["approval"] == "pending"
    assert len(first["orders"]) == 2
    assert first["reconciled"] is True
    assert run_paper_session(account, data_root=root, ledger_root=ledger_root, session_date="2025-01-02") == first

    approval = approve_paper_decision(
        account["account_id"],
        ledger_root=ledger_root,
        decision_id=first["decision_id"],
        approved=True,
    )
    assert approve_paper_decision(
        account["account_id"],
        ledger_root=ledger_root,
        decision_id=first["decision_id"],
        approved=True,
    ) == approval

    second = run_paper_session(account, data_root=root, ledger_root=ledger_root, session_date="2025-01-03")
    assert second["reconciled"] is True
    store = LedgerStore(ledger_root, account["account_id"])
    assert sum(event["event_type"] == "fill" for event in store.events()) == 2
    rebuilt = rebuild_paper_account(account["account_id"], ledger_root=ledger_root)
    assert rebuilt["event_count"] == len(store.events())
    assert rebuilt["nav_base"] == second["nav_base"]
    assert rebuilt["cash_base"] == second["cash_base"]
    assert run_paper_session(account, data_root=root, ledger_root=ledger_root, session_date="2025-01-03") == second


def test_risk_rejection_is_persisted_without_orders(tmp_path: Path) -> None:
    from research.paper import LedgerStore, create_paper_account, run_paper_session

    account, root = _account_fixture(tmp_path, risk_overrides={"max_position_weight": "0.4"})
    ledger_root = root / "ledger"
    create_paper_account(account, ledger_root=ledger_root)
    report = run_paper_session(account, data_root=root, ledger_root=ledger_root, session_date="2025-01-02")

    assert report["decision_status"] == "rejected"
    assert report["risk"]["status"] == "rejected"
    assert "max_position_weight" in report["risk"]["codes"]
    assert report["orders"] == []
    assert not any(event["event_type"] == "order" for event in LedgerStore(ledger_root, account["account_id"]).events())


def test_fractional_rebalance_does_not_false_reject_cash_reserve(tmp_path: Path) -> None:
    from research.paper import approve_paper_decision, create_paper_account, run_paper_session

    account, root = _account_fixture(tmp_path, risk_overrides={"minimum_cash_reserve": "0.1"})
    ledger_root = root / "ledger"
    create_paper_account(account, ledger_root=ledger_root)
    first = run_paper_session(
        account, data_root=root, ledger_root=ledger_root, session_date="2025-01-02"
    )
    approve_paper_decision(
        account["account_id"],
        ledger_root=ledger_root,
        decision_id=first["decision_id"],
        approved=True,
    )
    second = run_paper_session(
        account, data_root=root, ledger_root=ledger_root, session_date="2025-01-03"
    )

    assert second["decision_status"] != "rejected"
    assert "minimum_cash_reserve" not in second["risk"]["codes"]


def test_unavailable_close_halts_decision_without_looking_ahead(tmp_path: Path) -> None:
    from research.paper import LedgerStore, create_paper_account, run_paper_session

    account, root = _account_fixture(tmp_path)
    prices_path = root / "inputs" / "prices.json"
    prices = json.loads(prices_path.read_text(encoding="utf-8"))
    prices["available_at"] = "2025-01-03T00:00:00Z"
    for row in prices["rows"]:
        row["available_at"] = prices["available_at"]
    content = canonical_json(prices) + b"\n"
    prices_path.write_bytes(content)
    price_ref = next(reference for reference in account["inputs"] if reference["kind"] == "prices")
    price_ref["available_at"] = prices["available_at"]
    price_ref["sha256"] = hashlib.sha256(content).hexdigest()

    ledger_root = root / "ledger"
    create_paper_account(account, ledger_root=ledger_root)
    report = run_paper_session(account, data_root=root, ledger_root=ledger_root, session_date="2025-01-02")

    assert report["decision_status"] == "halted"
    assert report["risk"]["status"] == "halted"
    assert any("missing" in warning for warning in report["warnings"])
    assert any(event["event_type"] == "halt" for event in LedgerStore(ledger_root, account["account_id"]).events())


def test_ledger_backup_restore_rebuilds_the_same_projection(tmp_path: Path) -> None:
    from research.paper import (
        approve_paper_decision,
        create_paper_account,
        rebuild_paper_account,
        run_paper_session,
    )

    account, root = _account_fixture(tmp_path)
    ledger_root = root / "ledger"
    create_paper_account(account, ledger_root=ledger_root)
    first = run_paper_session(account, data_root=root, ledger_root=ledger_root, session_date="2025-01-02")
    approve_paper_decision(account["account_id"], ledger_root=ledger_root, decision_id=first["decision_id"], approved=True)
    run_paper_session(account, data_root=root, ledger_root=ledger_root, session_date="2025-01-03")

    restored_root = tmp_path / "restored"
    shutil.copytree(ledger_root, restored_root)
    assert rebuild_paper_account(account["account_id"], ledger_root=restored_root) == rebuild_paper_account(
        account["account_id"], ledger_root=ledger_root
    )


def test_paper_cli_dispatches_operator_lifecycle(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    from research.paper_cli import main

    account, root = _account_fixture(tmp_path)
    spec_path = root / "account.json"
    spec_path.write_bytes(canonical_json(account) + b"\n")
    ledger_root = root / "ledger"

    assert main(["create-account", "--spec", str(spec_path), "--ledger-root", str(ledger_root)]) == 0
    capsys.readouterr()
    assert main(
        [
            "run",
            "--spec",
            str(spec_path),
            "--data-root",
            str(root),
            "--ledger-root",
            str(ledger_root),
            "--session-date",
            "2025-01-02",
        ]
    ) == 0
    first = json.loads(capsys.readouterr().out)
    assert first["decision_status"] == "proposed"

    assert main(
        [
            "approve",
            "--account-id",
            account["account_id"],
            "--decision-id",
            first["decision_id"],
            "--ledger-root",
            str(ledger_root),
            "--approved",
        ]
    ) == 0
    capsys.readouterr()
    assert main(["rebuild", "--account-id", account["account_id"], "--ledger-root", str(ledger_root)]) == 0
    rebuilt = json.loads(capsys.readouterr().out)
    assert rebuilt["event_count"] > 1
    assert main(["reconcile", "--account-id", account["account_id"], "--ledger-root", str(ledger_root)]) == 0
    assert json.loads(capsys.readouterr().out)["status"] == "passed"
