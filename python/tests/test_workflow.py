from __future__ import annotations

import hashlib
import json
from datetime import UTC, datetime, timedelta
from pathlib import Path

import pytest

from research.workflow import (
    WorkflowValidationError,
    build_workflow_report,
    read_workflow_report,
    validate_workflow_spec,
    write_workflow_report,
)

THEME_ID = "10000000-0000-4000-8000-000000000001"
PACK_ID = "b93600db-7ee8-52f6-89d2-d0b30746f649"
HYPOTHESIS_ID = "72000000-0000-4000-8000-000000000001"
PREDICTION_ID = "73000000-0000-4000-8000-000000000001"
ACCOUNT_A = "40000000-0000-4000-8000-000000000101"
ACCOUNT_B = "40000000-0000-4000-8000-000000000102"
EXPERIMENT_EQUAL = "871cbdc1-c2d3-5565-8b02-b811376506b4"
EXPERIMENT_MOMENTUM = "5b966ae6-aa0b-57bc-945b-a2fe5f4e70e8"
EXPERIMENT_BRAZIL = "7facfaac-51f7-5371-8749-7b92939ef859"


def _write_json(path: Path, value: object) -> None:
    path.write_text(json.dumps(value, sort_keys=True) + "\n", encoding="utf-8")


def _ref(path: Path, value: object) -> dict[str, str]:
    _write_json(path, value)
    return {
        "path": path.name,
        "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
    }


def _source_reports(tmp_path: Path) -> tuple[dict[str, str], dict[str, str], dict[str, str]]:
    research = _ref(
        tmp_path / "research.json",
        {
        "decision_at": "2026-08-29T12:00:00Z",
        "evidence_pack_id": PACK_ID,
            "future_reference_rejected": True,
            "hypothesis_id": HYPOTHESIS_ID,
            "prediction_id": PREDICTION_ID,
            "theme_id": THEME_ID,
        },
    )
    backtest = _ref(
        tmp_path / "backtest.json",
        {
            "bias_audit": {"status": "passed"},
            "experiments": [
                {"experiment_id": "871cbdc1-c2d3-5565-8b02-b811376506b4", "name": "us-equal-weight-zero", "region": "US"},
                {"experiment_id": "5b966ae6-aa0b-57bc-945b-a2fe5f4e70e8", "name": "us-momentum-zero", "region": "US"},
                {"experiment_id": "7facfaac-51f7-5371-8749-7b92939ef859", "name": "br-equal-weight-conservative", "region": "BR"},
            ],
            "fixtures": {"brazil_reporting_currency": "USD", "macro_revisions": 2},
            "reproduction": {
                "artifact_files_equal": True,
                "manifest_equal": True,
                "perturbation_requires_new_identity": True,
            },
            "status": "passed",
        },
    )
    paper = _ref(
        tmp_path / "paper.json",
        {
            "acceptance": {
                "backup_restore": True,
                "duplicate_auto_cycle": True,
                "forward_sessions": True,
                "rebuild_exact": True,
                "reconciliation": True,
            },
            "accounts": [{"account_id": ACCOUNT_A}, {"account_id": ACCOUNT_B}],
            "status": "passed",
        },
    )
    return research, backtest, paper


def _spec(tmp_path: Path, *, scenario: str = "thematic", forward: dict | None = None) -> dict:
    research, backtest, paper = _source_reports(tmp_path)
    return {
        "$schema": "../schemas/research-workflow.schema.json",
        "schema_version": "1.0.0",
        "workflow_id": "80000000-0000-4000-8000-000000000001",
        "scenario": scenario,
        "decision_at": "2026-08-29T12:00:00Z",
        "research_report": research,
        "backtest_report": backtest,
        "backtest_experiment_ids": [EXPERIMENT_EQUAL, EXPERIMENT_MOMENTUM, EXPERIMENT_BRAZIL],
        "paper_report": paper,
        "paper_account_ids": [ACCOUNT_A, ACCOUNT_B] if scenario == "cross_market" else [ACCOUNT_A],
        "forward_record": forward or {"status": "recorded_replay", "evidence": None},
        "commodity_evidence": None,
        "limitations": [],
    }


def _paper_account() -> dict[str, object]:
    security_id = "10000000-0000-4000-8000-000000000201"
    return {
        "schema_version": "1.0.0",
        "account_id": ACCOUNT_A,
        "name": "workflow forward account",
        "strategy": {
            "name": "equal_weight",
            "version": "1.0.0",
            "git_commit": "1" * 40,
            "parameters": {"rebalance_frequency": "daily"},
        },
        "period": {"start_date": "2020-01-01", "end_date": "2030-01-01"},
        "universe": {
            "universe_id": "20000000-0000-4000-8000-000000000201",
            "version": "1.0.0",
            "security_ids": [security_id],
            "membership_fingerprint": "a" * 64,
        },
        "security_metadata": [
            {"security_id": security_id, "country": "US", "sector": "technology", "themes": ["ai"]}
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
            for index, kind in enumerate(("prices", "calendar", "membership"), start=1)
        ],
        "benchmark": {"security_id": security_id, "currency": "USD"},
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


def _forward_evidence(tmp_path: Path, *, session_date: str | None = None) -> dict[str, str]:
    session_date = session_date or (datetime.now(UTC).date() - timedelta(days=1)).isoformat()
    captured_at = datetime.now(UTC).replace(microsecond=0)
    account_dir = tmp_path / "ledger" / "accounts" / ACCOUNT_A
    reports_dir = account_dir / "reports"
    account_path = account_dir / "account.json"
    report_path = reports_dir / f"report-{session_date}.json"
    manifest_path = account_dir / "ledger-manifest.json"
    from research.paper import create_paper_account

    create_paper_account(_paper_account(), ledger_root=tmp_path / "ledger")
    _write_json(
        report_path,
        {
            "schema_version": "1.0.0",
            "report_id": "50000000-0000-4000-8000-000000000001",
            "account_id": ACCOUNT_A,
            "session_date": session_date,
            "recorded_at": captured_at.isoformat().replace("+00:00", "Z"),
            "decision_id": "50000000-0000-4000-8000-000000000003",
            "input_fingerprint": "c" * 64,
            "decision_status": "no_op",
            "risk": {
                "status": "approved",
                "policy_version": "1.0.0",
                "codes": [],
                "reasons": [],
                "checked_at": (captured_at - timedelta(minutes=1)).isoformat().replace("+00:00", "Z"),
            },
            "approval": "not_required",
            "orders": [],
            "nav_base": "10000",
            "cash_base": "10000",
            "positions_value_base": "0",
            "gross_exposure": "0",
            "drawdown": "0",
            "ledger_sequence_start": 1,
            "ledger_sequence_end": 1,
            "reconciled": True,
        },
    )
    def relative(path: Path) -> str:
        return path.relative_to(tmp_path).as_posix()

    evidence = {
        "$schema": "../schemas/paper-forward-record.schema.json",
        "schema_version": "1.0.0",
        "capture_method": "invs-paper-forward-capture",
        "record_id": "50000000-0000-4000-8000-000000000002",
        "account_ids": [ACCOUNT_A],
        "session_dates": [session_date],
        "sessions": 1,
        "wall_clock": True,
        "started_at": (captured_at - timedelta(minutes=1)).isoformat().replace("+00:00", "Z"),
        "captured_at": captured_at.isoformat().replace("+00:00", "Z"),
        "fitness": "current_research_only",
        "observations": [
            {
                "account_id": ACCOUNT_A,
                "session_date": session_date,
                "account_path": relative(account_path),
                "account_sha256": hashlib.sha256(account_path.read_bytes()).hexdigest(),
                "report_path": relative(report_path),
                "report_sha256": hashlib.sha256(report_path.read_bytes()).hexdigest(),
                "report_id": "50000000-0000-4000-8000-000000000001",
                "ledger_manifest_path": relative(manifest_path),
                "ledger_manifest_sha256": hashlib.sha256(manifest_path.read_bytes()).hexdigest(),
                "ledger_sequence_start": 1,
                "ledger_sequence_end": 1,
                "reconciled": True,
            }
        ],
    }
    return _ref(tmp_path / "forward.json", evidence)


def test_workflow_report_links_existing_layers_and_preserves_replay_limit(tmp_path: Path) -> None:
    spec = _spec(tmp_path)
    report = build_workflow_report(spec, repo_root=tmp_path)

    assert report["status"] == "attention"
    assert report["forward_record_status"] == "recorded_replay"
    assert report["links"]["hypothesis_id"] == HYPOTHESIS_ID
    assert {check["check_id"] for check in report["checks"]} >= {
        "research-chain",
        "backtest-bias-audit",
        "paper-reconciliation",
        "forward-record",
    }
    assert any("recorded/replayed" in limitation for limitation in report["limitations"])

    output = tmp_path / "workflow-report.json"
    assert write_workflow_report(report, path=output, repo_root=tmp_path) == output
    assert write_workflow_report(report, path=output, repo_root=tmp_path) == output
    assert read_workflow_report(output, repo_root=tmp_path, spec=spec).report == report


def test_cross_market_report_requires_regions_and_labels_commodity_fitness(tmp_path: Path) -> None:
    spec = _spec(tmp_path, scenario="cross_market")
    commodity = _ref(
        tmp_path / "commodity.json",
        {
            "fitness": "installation_replay_only",
            "source": "bounded-copper-fixture",
        },
    )
    spec["commodity_evidence"] = commodity

    report = build_workflow_report(spec, repo_root=tmp_path)

    assert report["status"] == "attention"
    assert {"US", "BR"}.issubset(report["links"]["regions"])
    assert {entry["dataset"] for entry in report["fitness"]} == {"commodity-evidence", "paper-record"}
    assert any(check["check_id"] == "commodity-fitness" and check["status"] == "attention" for check in report["checks"])


def test_cross_market_report_fails_without_commodity_evidence(tmp_path: Path) -> None:
    report = build_workflow_report(_spec(tmp_path, scenario="cross_market"), repo_root=tmp_path)

    assert report["status"] == "failed"
    assert any(check["check_id"] == "commodity-evidence" and check["status"] == "failed" for check in report["checks"])


def test_genuine_forward_evidence_can_satisfy_the_workflow_gate(tmp_path: Path) -> None:
    forward = _forward_evidence(tmp_path)
    spec = _spec(tmp_path, forward={"status": "genuine", "evidence": forward})

    report = build_workflow_report(spec, repo_root=tmp_path)

    assert report["status"] == "passed"
    assert report["checks"][-1]["check_id"] == "forward-record"
    assert report["checks"][-1]["status"] == "passed"


def test_genuine_forward_evidence_rejects_stale_session(tmp_path: Path) -> None:
    forward = _forward_evidence(tmp_path, session_date="2025-01-01")
    spec = _spec(tmp_path, forward={"status": "genuine", "evidence": forward})

    with pytest.raises(WorkflowValidationError, match="older than"):
        validate_workflow_spec(spec, repo_root=tmp_path)


def test_workflow_rejects_changed_source_hash(tmp_path: Path) -> None:
    spec = _spec(tmp_path)
    research = tmp_path / "research.json"
    research.write_text(research.read_text(encoding="utf-8") + "\n", encoding="utf-8")

    with pytest.raises(WorkflowValidationError, match="research_report.sha256"):
        validate_workflow_spec(spec, repo_root=tmp_path)
