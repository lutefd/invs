from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta
from pathlib import Path

import pytest

from research.forward_record import (
    ForwardRecordError,
    capture_forward_record,
    load_forward_record,
)
from research.paper import create_paper_account

ACCOUNT_ID = "40000000-0000-4000-8000-000000000201"
SECURITY_ID = "10000000-0000-4000-8000-000000000201"
UNIVERSE_ID = "20000000-0000-4000-8000-000000000201"


def _account() -> dict[str, object]:
    artifact_kinds = ("prices", "calendar", "membership")
    return {
        "schema_version": "1.0.0",
        "account_id": ACCOUNT_ID,
        "name": "forward capture test account",
        "strategy": {
            "name": "equal_weight",
            "version": "1.0.0",
            "git_commit": "1" * 40,
            "parameters": {"rebalance_frequency": "daily"},
        },
        "period": {"start_date": "2020-01-01", "end_date": "2030-01-01"},
        "universe": {
            "universe_id": UNIVERSE_ID,
            "version": "1.0.0",
            "security_ids": [SECURITY_ID],
            "membership_fingerprint": "a" * 64,
        },
        "security_metadata": [
            {"security_id": SECURITY_ID, "country": "US", "sector": "technology", "themes": ["ai"]}
        ],
        "inputs": [
            {
                "kind": kind,
                "artifact_id": f"30000000-0000-4000-8000-00000000020{index}",
                "path": f"inputs/{kind}.json",
                "sha256": "b" * 64,
                "available_at": "2020-01-01T00:00:00Z",
                "fitness": "current_research_only",
            }
            for index, kind in enumerate(artifact_kinds, start=1)
        ],
        "benchmark": {"security_id": SECURITY_ID, "currency": "USD"},
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
        "risk_policy": {
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
        },
        "approval_policy": {"mode": "auto"},
        "missing_data_policy": "halt_decision",
    }


def _write_report(ledger_root: Path, session_date: str) -> None:
    report_dir = ledger_root / "accounts" / ACCOUNT_ID / "reports"
    report_dir.mkdir(parents=True, exist_ok=True)
    report = {
        "schema_version": "1.0.0",
        "report_id": "50000000-0000-4000-8000-000000000201",
        "account_id": ACCOUNT_ID,
        "session_date": session_date,
        "ledger_sequence_start": 1,
        "ledger_sequence_end": 1,
        "reconciled": True,
    }
    (report_dir / f"report-{session_date}.json").write_text(
        json.dumps(report, sort_keys=True) + "\n", encoding="utf-8"
    )


def test_capture_binds_recent_reconciled_ledger_to_forward_evidence(tmp_path: Path) -> None:
    ledger_root = tmp_path / "ledger"
    create_paper_account(_account(), ledger_root=ledger_root)
    session_date = (datetime.now(UTC).date() - timedelta(days=1)).isoformat()
    _write_report(ledger_root, session_date)

    output = capture_forward_record(
        repo_root=tmp_path,
        ledger_root=ledger_root,
        account_ids=[ACCOUNT_ID],
        output=tmp_path / "forward-record.json",
    )

    evidence = load_forward_record(output, repo_root=tmp_path)
    assert evidence["account_ids"] == [ACCOUNT_ID]
    assert evidence["session_dates"] == [session_date]
    assert evidence["fitness"] == "current_research_only"
    assert evidence["observations"][0]["reconciled"] is True


def test_forward_evidence_rejects_old_session_even_with_valid_file_hashes(tmp_path: Path) -> None:
    ledger_root = tmp_path / "ledger"
    create_paper_account(_account(), ledger_root=ledger_root)
    session_date = (datetime.now(UTC).date() - timedelta(days=1)).isoformat()
    _write_report(ledger_root, session_date)
    output = capture_forward_record(
        repo_root=tmp_path,
        ledger_root=ledger_root,
        account_ids=[ACCOUNT_ID],
        output=tmp_path / "forward-record.json",
    )

    evidence = json.loads(output.read_text(encoding="utf-8"))
    evidence["session_dates"] = ["2020-01-01"]
    evidence["observations"][0]["session_date"] = "2020-01-01"
    output.write_text(json.dumps(evidence, sort_keys=True) + "\n", encoding="utf-8")

    with pytest.raises(ForwardRecordError, match="older than"):
        load_forward_record(output, repo_root=tmp_path)
