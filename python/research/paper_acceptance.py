"""Deterministic v0.6 forward-paper acceptance drill."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
from collections import Counter
from datetime import date, timedelta
from pathlib import Path
from typing import Any

from .experiments import canonical_json
from .paper import LedgerStore

SECURITY_A = "10000000-0000-4000-8000-000000000001"
SECURITY_B = "10000000-0000-4000-8000-000000000002"
UNIVERSE_ID = "20000000-0000-4000-8000-000000000001"
PROMOTED_EXPERIMENT_ID = "10000000-0000-4000-8000-000000000001"
PERIOD_START = date(2025, 1, 2)
PERIOD_END = date(2025, 1, 31)
ARTIFACT_AVAILABLE_AT = "2025-01-31T23:59:00Z"

ACCOUNT_EQUAL = "40000000-0000-4000-8000-000000000101"
ACCOUNT_MOMENTUM = "40000000-0000-4000-8000-000000000102"
ACCOUNT_STALE = "40000000-0000-4000-8000-000000000103"
ACCOUNT_RISK = "40000000-0000-4000-8000-000000000104"


def _write_immutable(path: Path, document: dict[str, Any]) -> None:
    content = canonical_json(document) + b"\n"
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        if path.read_bytes() != content:
            raise RuntimeError(f"acceptance artifact already contains different bytes: {path}")
        return
    temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    try:
        temporary.write_bytes(content)
        temporary.replace(path)
    finally:
        if temporary.exists():
            temporary.unlink()


def _sessions() -> tuple[date, ...]:
    result: list[date] = []
    current = PERIOD_START
    while current <= PERIOD_END:
        if current.weekday() < 5:
            result.append(current)
        current += timedelta(days=1)
    return tuple(result)


def _price_row(
    security_id: str,
    session_date: date,
    open_price: str,
    close: str,
) -> dict[str, Any]:
    high = max(open_price, close, key=lambda value: float(value))
    low = min(open_price, close, key=lambda value: float(value))
    return {
        "security_id": security_id,
        "session_date": session_date.isoformat(),
        "observed_at": f"{session_date.isoformat()}T12:59:00Z",
        "available_at": f"{session_date.isoformat()}T13:00:00Z",
        "currency": "USD",
        "price_basis": "raw",
        "open": open_price,
        "high": high,
        "low": low,
        "close": close,
        "volume": "1000000",
        "has_volume": True,
    }


def _main_prices(sessions: tuple[date, ...]) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    previous_a: str | None = None
    previous_b: str | None = None
    for index, session_date in enumerate(sessions):
        if index < 10:
            close_a = str(100 + (index * 2))
        else:
            close_a = str(60 + ((index - 10) * 2))
        close_b = str(80 + index)
        open_a = close_a if previous_a is None or index == 10 else previous_a
        open_b = close_b if previous_b is None else previous_b
        rows.extend(
            [
                _price_row(SECURITY_A, session_date, open_a, close_a),
                _price_row(SECURITY_B, session_date, open_b, close_b),
            ]
        )
        previous_a = close_a
        previous_b = close_b
    return rows


def _calendar_rows(sessions: tuple[date, ...]) -> list[dict[str, Any]]:
    return [
        {
            "session_date": session_date.isoformat(),
            "open_at": f"{session_date.isoformat()}T14:30:00Z",
            "close_at": f"{session_date.isoformat()}T21:00:00Z",
            "available_at": f"{session_date.isoformat()}T00:01:00Z",
            "session_status": "open",
        }
        for session_date in sessions
    ]


def _membership_rows() -> list[dict[str, Any]]:
    return [
        {
            "security_id": SECURITY_A,
            "valid_from": "2025-01-01",
            "valid_until": None,
            "member": True,
            "available_at": "2025-01-01T00:01:00Z",
            "revision": 0,
        },
        {
            "security_id": SECURITY_B,
            "valid_from": "2025-01-01",
            "valid_until": "2025-01-16",
            "member": True,
            "available_at": "2025-01-01T00:01:00Z",
            "revision": 0,
        },
        {
            "security_id": SECURITY_B,
            "valid_from": "2025-01-16",
            "valid_until": None,
            "member": False,
            "available_at": "2025-01-15T13:00:00Z",
            "revision": 1,
        },
    ]


def _corporate_action_rows() -> list[dict[str, Any]]:
    available_at = "2025-01-15T13:00:00Z"
    return [
        {
            "id": "60000000-0000-4000-8000-000000000001",
            "security_id": SECURITY_A,
            "action_type": "split",
            "effective_date": "2025-01-16",
            "payment_date": None,
            "available_at": available_at,
            "currency": "USD",
            "ratio_numerator": "2",
            "ratio_denominator": "1",
            "cash_amount": "0",
            "settlement_price": None,
            "target_security_id": None,
        },
        {
            "id": "60000000-0000-4000-8000-000000000002",
            "security_id": SECURITY_A,
            "action_type": "cash_dividend",
            "effective_date": "2025-01-16",
            "payment_date": "2025-01-16",
            "available_at": available_at,
            "currency": "USD",
            "ratio_numerator": "0",
            "ratio_denominator": "0",
            "cash_amount": "1",
            "settlement_price": None,
            "target_security_id": None,
        },
        {
            "id": "60000000-0000-4000-8000-000000000003",
            "security_id": SECURITY_B,
            "action_type": "delisting",
            "effective_date": "2025-01-16",
            "payment_date": None,
            "available_at": available_at,
            "currency": "USD",
            "ratio_numerator": "0",
            "ratio_denominator": "0",
            "cash_amount": "0",
            "settlement_price": "90",
            "target_security_id": None,
        },
    ]


def _publish_artifact(
    root: Path,
    kind: str,
    artifact_id: str,
    filename: str,
    rows: list[dict[str, Any]],
) -> dict[str, Any]:
    document = {
        "schema_version": "1.0.0",
        "artifact_kind": kind,
        "artifact_id": artifact_id,
        "available_at": ARTIFACT_AVAILABLE_AT,
        "rows": rows,
    }
    path = root / "inputs" / filename
    _write_immutable(path, document)
    return {
        "kind": kind,
        "artifact_id": artifact_id,
        "path": f"inputs/{filename}",
        "sha256": hashlib.sha256(canonical_json(document) + b"\n").hexdigest(),
        "available_at": ARTIFACT_AVAILABLE_AT,
        "fitness": "current_research_only",
    }


def _publish_inputs(root: Path) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    sessions = _sessions()
    prices = _main_prices(sessions)
    main_refs = [
        _publish_artifact(
            root,
            "prices",
            "30000000-0000-4000-8000-000000000011",
            "prices.json",
            prices,
        ),
        _publish_artifact(
            root,
            "calendar",
            "30000000-0000-4000-8000-000000000012",
            "calendar.json",
            _calendar_rows(sessions),
        ),
        _publish_artifact(
            root,
            "membership",
            "30000000-0000-4000-8000-000000000013",
            "membership.json",
            _membership_rows(),
        ),
        _publish_artifact(
            root,
            "corporate_actions",
            "30000000-0000-4000-8000-000000000014",
            "corporate-actions.json",
            _corporate_action_rows(),
        ),
    ]
    stale_prices = [
        row
        for row in prices
        if row["session_date"] != PERIOD_START.isoformat()
    ]
    stale_refs = [
        _publish_artifact(
            root,
            "prices",
            "30000000-0000-4000-8000-000000000015",
            "stale-prices.json",
            stale_prices,
        ),
        main_refs[1],
        main_refs[2],
    ]
    return main_refs, stale_refs


def _account(
    account_id: str,
    name: str,
    strategy_name: str,
    approval_mode: str,
    input_refs: list[dict[str, Any]],
    *,
    max_position_weight: str = "1",
) -> dict[str, Any]:
    if strategy_name == "momentum_12_1":
        parameters: dict[str, Any] = {
            "lookback_sessions": 4,
            "skip_sessions": 1,
            "top_k": 1,
            "rebalance_frequency": "weekly",
        }
        git_commit = "2222222222222222222222222222222222222222"
    else:
        parameters = {"rebalance_frequency": "weekly"}
        git_commit = "1111111111111111111111111111111111111111"
    return {
        "schema_version": "1.0.0",
        "account_id": account_id,
        "name": name,
        "strategy": {
            "name": strategy_name,
            "version": "1.0.0",
            "git_commit": git_commit,
            "parameters": parameters,
        },
        "period": {
            "start_date": PERIOD_START.isoformat(),
            "end_date": PERIOD_END.isoformat(),
        },
        "universe": {
            "universe_id": UNIVERSE_ID,
            "version": "1.0.0",
            "security_ids": [SECURITY_B, SECURITY_A],
            "membership_fingerprint": "a" * 64,
        },
        "security_metadata": [
            {
                "security_id": SECURITY_A,
                "country": "US",
                "sector": "technology",
                "themes": ["ai"],
            },
            {
                "security_id": SECURITY_B,
                "country": "US",
                "sector": "financials",
                "themes": ["banking"],
            },
        ],
        "inputs": input_refs,
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
            "rebalance_frequency": "weekly",
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
            "version": "1.0.0",
            "max_gross_exposure": "1",
            "max_position_weight": max_position_weight,
            "max_sector_exposure": "1",
            "max_country_exposure": "1",
            "max_currency_exposure": "1",
            "max_theme_exposure": "1",
            "minimum_cash_reserve": "0.04",
            "max_turnover": "1",
            "max_daily_notional": "100000",
            "max_price_gap": "1",
            "max_stale_sessions": 10,
            "max_participation": "1",
            "max_drawdown": "1",
            "prohibited_security_ids": [],
        },
        "approval_policy": {"mode": approval_mode},
        "missing_data_policy": "halt_decision",
        "promoted_backtest": {
            "experiment_id": PROMOTED_EXPERIMENT_ID,
            "manifest_path": "backtest/v0.5/promoted-manifest.json",
            "manifest_sha256": "b" * 64,
        },
    }


def _cli(root: Path, *arguments: str) -> dict[str, Any]:
    environment = os.environ.copy()
    package_root = str(Path(__file__).resolve().parents[1])
    environment["PYTHONPATH"] = os.pathsep.join(
        part for part in (package_root, environment.get("PYTHONPATH")) if part
    )
    result = subprocess.run(
        [sys.executable, "-m", "research.paper_cli", *arguments],
        cwd=root,
        env=environment,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        raise RuntimeError(
            f"paper CLI failed ({' '.join(arguments)}): {result.stderr.strip()}"
        )
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"paper CLI returned invalid JSON: {result.stdout!r}") from error
    if not isinstance(value, dict):
        raise TypeError("paper CLI returned a non-object JSON value")
    return value


def _write_spec(root: Path, account: dict[str, Any]) -> Path:
    path = root / "specs" / f"{account['account_id']}.json"
    _write_immutable(path, account)
    return path


def _run_account(
    root: Path,
    account: dict[str, Any],
    sessions: tuple[date, ...],
    *,
    duplicate_first: bool = False,
) -> tuple[list[dict[str, Any]], dict[str, bool]]:
    spec_path = _write_spec(root, account)
    ledger_root = root / "ledger"
    _cli(root, "create-account", "--spec", str(spec_path), "--ledger-root", str(ledger_root))
    reports: list[dict[str, Any]] = []
    duplicate_proposal = False
    duplicate_approval = False
    for index, session_date in enumerate(sessions):
        arguments = (
            "run",
            "--spec",
            str(spec_path),
            "--data-root",
            str(root),
            "--ledger-root",
            str(ledger_root),
            "--session-date",
            session_date.isoformat(),
        )
        report = _cli(root, *arguments)
        if index == 0 and duplicate_first:
            duplicate_proposal = _cli(root, *arguments) == report
        if report["approval"] == "pending":
            approval_args = (
                "approve",
                "--account-id",
                account["account_id"],
                "--decision-id",
                report["decision_id"],
                "--ledger-root",
                str(ledger_root),
                "--approved",
            )
            approval = _cli(root, *approval_args)
            if index == 0 and duplicate_first:
                duplicate_approval = _cli(root, *approval_args) == approval
        reports.append(report)
    return reports, {
        "proposal_repeat_identical": duplicate_proposal,
        "approval_repeat_identical": duplicate_approval,
    }


def _assert_tree_equal(left: Path, right: Path) -> None:
    left_files = sorted(path.relative_to(left) for path in left.rglob("*") if path.is_file())
    right_files = sorted(path.relative_to(right) for path in right.rglob("*") if path.is_file())
    if left_files != right_files:
        raise RuntimeError("backup file set differs from the source ledger")
    for relative in left_files:
        if (left / relative).read_bytes() != (right / relative).read_bytes():
            raise RuntimeError(f"backup file differs from source ledger: {relative}")


def _account_summary(
    account: dict[str, Any], reports: list[dict[str, Any]], events: tuple[dict[str, Any], ...]
) -> dict[str, Any]:
    counts = Counter(event["event_type"] for event in events)
    return {
        "account_id": account["account_id"],
        "strategy": account["strategy"]["name"],
        "strategy_version": account["strategy"]["version"],
        "sessions": len(reports),
        "rebalanced_sessions": sum(
            report["decision_status"] in {"proposed", "approved", "rejected"}
            for report in reports
        ),
        "no_op_sessions": sum(report["decision_status"] == "no_op" for report in reports),
        "filled_orders": counts["fill"],
        "event_count": len(events),
        "event_types": dict(sorted(counts.items())),
        "final_report": reports[-1],
    }


def run_acceptance(acceptance_root: str | Path) -> dict[str, Any]:
    root = Path(acceptance_root).expanduser().resolve()
    root.mkdir(parents=True, exist_ok=True)
    sessions = _sessions()
    main_refs, stale_refs = _publish_inputs(root)

    equal = _account(
        ACCOUNT_EQUAL,
        "v0.6 acceptance equal weight",
        "equal_weight",
        "manual",
        main_refs,
    )
    momentum = _account(
        ACCOUNT_MOMENTUM,
        "v0.6 acceptance momentum",
        "momentum_12_1",
        "auto",
        main_refs,
    )
    stale = _account(
        ACCOUNT_STALE,
        "v0.6 acceptance stale gate",
        "equal_weight",
        "manual",
        stale_refs,
    )
    risk = _account(
        ACCOUNT_RISK,
        "v0.6 acceptance risk gate",
        "equal_weight",
        "manual",
        main_refs,
        max_position_weight="0.4",
    )

    equal_reports, restart = _run_account(root, equal, sessions, duplicate_first=True)
    momentum_reports, momentum_restart = _run_account(root, momentum, sessions, duplicate_first=True)

    stale_spec = _write_spec(root, stale)
    stale_ledger = root / "ledger"
    _cli(root, "create-account", "--spec", str(stale_spec), "--ledger-root", str(stale_ledger))
    stale_report = _cli(
        root,
        "run",
        "--spec",
        str(stale_spec),
        "--data-root",
        str(root),
        "--ledger-root",
        str(stale_ledger),
        "--session-date",
        PERIOD_START.isoformat(),
    )

    risk_spec = _write_spec(root, risk)
    _cli(root, "create-account", "--spec", str(risk_spec), "--ledger-root", str(stale_ledger))
    risk_report = _cli(
        root,
        "run",
        "--spec",
        str(risk_spec),
        "--data-root",
        str(root),
        "--ledger-root",
        str(stale_ledger),
        "--session-date",
        PERIOD_START.isoformat(),
    )

    ledger_root = root / "ledger"
    equal_store = LedgerStore(ledger_root, ACCOUNT_EQUAL)
    momentum_store = LedgerStore(ledger_root, ACCOUNT_MOMENTUM)
    stale_store = LedgerStore(ledger_root, ACCOUNT_STALE)
    risk_store = LedgerStore(ledger_root, ACCOUNT_RISK)
    equal_events = equal_store.events()
    momentum_events = momentum_store.events()
    stale_events = stale_store.events()
    risk_events = risk_store.events()

    action_types = {
        event["details"].get("action_type")
        for event in equal_events
        if event["event_type"] in {"dividend", "split", "delisting"}
    }
    required_actions = {"cash_dividend", "split", "delisting"}
    if not required_actions.issubset(action_types):
        raise RuntimeError(f"acceptance ledger did not apply all actions: {action_types}")
    if not any(report["decision_status"] == "no_op" for report in equal_reports):
        raise RuntimeError("equal-weight sleeve did not produce a no-op session")
    if not any(event["event_type"] == "fill" for event in equal_events):
        raise RuntimeError("equal-weight sleeve did not fill an approved order")
    if not any(report["orders"] for report in momentum_reports):
        raise RuntimeError("momentum sleeve did not publish an order")
    if any(event["event_type"] == "halt" for event in (*equal_events, *momentum_events)):
        raise RuntimeError("main strategy sleeves contained an unintended halt")
    if any(
        report["decision_status"] == "rejected"
        for report in (*equal_reports, *momentum_reports)
    ):
        raise RuntimeError("main strategy sleeves contained an unintended risk rejection")
    if stale_report["decision_status"] != "halted" or not any(
        event["event_type"] == "halt" for event in stale_events
    ):
        raise RuntimeError("stale-data probe did not halt")
    if (
        risk_report["decision_status"] != "rejected"
        or "max_position_weight" not in risk_report["risk"]["codes"]
        or any(event["event_type"] == "order" for event in risk_events)
    ):
        raise RuntimeError("risk probe did not reject without orders")

    equal_rebuild = _cli(
        root,
        "rebuild",
        "--account-id",
        ACCOUNT_EQUAL,
        "--ledger-root",
        str(ledger_root),
    )
    momentum_rebuild = _cli(
        root,
        "rebuild",
        "--account-id",
        ACCOUNT_MOMENTUM,
        "--ledger-root",
        str(ledger_root),
    )
    equal_report = equal_reports[-1]
    momentum_report = momentum_reports[-1]
    for rebuilt, report in ((equal_rebuild, equal_report), (momentum_rebuild, momentum_report)):
        if any(
            rebuilt[field] != report[field]
            for field in ("nav_base", "cash_base", "positions_value_base")
        ):
            raise RuntimeError("rebuild projection differs from the final daily report")

    backup_root = root / "backup-ledger"
    if not backup_root.exists():
        shutil.copytree(ledger_root, backup_root)
    else:
        _assert_tree_equal(ledger_root, backup_root)
    restored_equal = _cli(
        root,
        "rebuild",
        "--account-id",
        ACCOUNT_EQUAL,
        "--ledger-root",
        str(backup_root),
    )
    restored_reconcile = _cli(
        root,
        "reconcile",
        "--account-id",
        ACCOUNT_EQUAL,
        "--ledger-root",
        str(backup_root),
    )
    if restored_equal != equal_rebuild or restored_reconcile["status"] != "passed":
        raise RuntimeError("backup restore did not reproduce the paper projection")

    equal_reconcile = _cli(
        root,
        "reconcile",
        "--account-id",
        ACCOUNT_EQUAL,
        "--ledger-root",
        str(ledger_root),
    )
    momentum_reconcile = _cli(
        root,
        "reconcile",
        "--account-id",
        ACCOUNT_MOMENTUM,
        "--ledger-root",
        str(ledger_root),
    )
    if equal_reconcile["status"] != "passed" or momentum_reconcile["status"] != "passed":
        raise RuntimeError("forward paper reconciliation failed")

    report = {
        "schema_version": "1.0.0",
        "status": "passed",
        "period": {
            "start_date": PERIOD_START.isoformat(),
            "end_date": PERIOD_END.isoformat(),
        },
        "session_count": len(sessions),
        "accounts": [
            _account_summary(equal, equal_reports, equal_events),
            _account_summary(momentum, momentum_reports, momentum_events),
        ],
        "acceptance": {
            "forward_sessions": len(sessions) >= 20,
            "separate_strategy_sleeves": {
                "equal_weight": equal["account_id"] != momentum["account_id"],
                "momentum_12_1": momentum["account_id"] != equal["account_id"],
            },
            "rebalance": any(report["orders"] for report in equal_reports),
            "no_op": any(report["decision_status"] == "no_op" for report in equal_reports),
            "stale_data_halt": stale_report["decision_status"] == "halted",
            "risk_rejection": risk_report["decision_status"] == "rejected",
            "dividend": "cash_dividend" in action_types,
            "corporate_actions": required_actions.issubset(action_types),
            "restart": restart["proposal_repeat_identical"] and restart["approval_repeat_identical"],
            "duplicate_auto_cycle": momentum_restart["proposal_repeat_identical"],
            "rebuild_exact": equal_rebuild == restored_equal,
            "backup_restore": restored_reconcile["status"] == "passed",
            "reconciliation": equal_reconcile["status"] == "passed"
            and momentum_reconcile["status"] == "passed",
        },
        "probes": {
            "stale_data": {
                "account_id": ACCOUNT_STALE,
                "decision_status": stale_report["decision_status"],
                "warnings": stale_report.get("warnings", []),
            },
            "risk": {
                "account_id": ACCOUNT_RISK,
                "decision_status": risk_report["decision_status"],
                "codes": risk_report["risk"]["codes"],
                "order_count": len(risk_report["orders"]),
            },
        },
        "corporate_actions": sorted(action_types),
        "rebuild": {
            "equal_weight": equal_rebuild,
            "momentum_12_1": momentum_rebuild,
            "backup_equal_weight": restored_equal,
        },
        "reconciliation": {
            "equal_weight": equal_reconcile,
            "momentum_12_1": momentum_reconcile,
            "backup_equal_weight": restored_reconcile,
        },
    }
    _write_immutable(root / "paper-reproduction.json", report)
    return report


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--acceptance-root", required=True, type=Path)
    args = parser.parse_args(argv)
    report = run_acceptance(args.acceptance_root)
    print(json.dumps(report, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
