from __future__ import annotations

import json
import os
from pathlib import Path

import pytest
from scripts import daily_cycle
from scripts.daily_cycle import DailyCycleError, build_plan, run_cycle, validate_cycle_spec

REPO_ROOT = Path(os.environ.get("INVS_REPO_ROOT", Path(__file__).resolve().parents[2]))


ACCOUNT_ID = "40000000-0000-4000-8000-000000000001"


def _write(path: Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value), encoding="utf-8")


def _paper_account() -> dict[str, object]:
    security_id = "10000000-0000-4000-8000-000000000001"
    return {
        "schema_version": "1.0.0",
        "account_id": ACCOUNT_ID,
        "name": "daily cycle test account",
        "strategy": {
            "name": "equal_weight",
            "version": "1.0.0",
            "git_commit": "unknown",
            "parameters": {"rebalance_frequency": "daily"},
        },
        "period": {"start_date": "2026-01-01", "end_date": "2026-12-31"},
        "universe": {
            "universe_id": "20000000-0000-4000-8000-000000000001",
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
                "artifact_id": f"30000000-0000-4000-8000-00000000000{index}",
                "path": f"inputs/{kind}.json",
                "sha256": "b" * 64,
                "available_at": "2026-01-01T00:00:00Z",
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


def _spec(tmp_path: Path) -> dict:
    for name in ("universe.json", "schedule.json", "calendar.json", "registry.json", "taxonomy.json"):
        _write(tmp_path / "inputs" / name, {})
    paper_path = tmp_path / "inputs" / "paper.json"
    _write(paper_path, _paper_account())
    return {
        "$schema": "../schemas/daily-cycle.schema.json",
        "schema_version": "1.0.0",
        "cycle_id": "daily-cycle-test",
        "session_date": "2026-08-29",
        "source": "all",
        "run_key": "daily-cycle-test",
        "data_root": "data",
        "ledger_root": "research/paper/ledger",
        "backup_dir": str(tmp_path.parent / "backup-daily-cycle-test"),
        "report_path": ".runtime/cycles/test.json",
        "log_dir": ".runtime/cycles/logs",
        "collection": {"enabled": True},
        "feature": {
            "enabled": True,
            "universe": "inputs/universe.json",
            "schedule": "inputs/schedule.json",
            "calendar_pin": "inputs/calendar.json",
            "registry": "inputs/registry.json",
            "taxonomy_registry": "inputs/taxonomy.json",
            "security_mappings": None,
            "feature_set": "market-basic",
            "feature_set_version": "1.0.0",
        },
        "paper": [{"account_id": ACCOUNT_ID, "spec": "inputs/paper.json"}],
    }


def test_cycle_spec_requires_all_operator_inputs(tmp_path: Path) -> None:
    spec = _spec(tmp_path)
    normalized = validate_cycle_spec(spec, repo_root=tmp_path)
    plan = build_plan(normalized)
    assert plan[0].name == "preflight"
    assert plan[1].name == "collection"
    assert plan[2].name == "reconcile-before-derived"
    assert plan[3].name == "feature-batch"
    assert plan[-2].name == "reconcile-after-derived"
    assert plan[-1].name == "observe"
    assert "paper:40000000-0000-4000-8000-000000000001:run" in {
        stage.name for stage in plan
    }


def test_cycle_spec_rejects_unknown_fields(tmp_path: Path) -> None:
    spec = _spec(tmp_path)
    spec["unexpected"] = True
    with pytest.raises(DailyCycleError):
        validate_cycle_spec(spec, repo_root=tmp_path)


def test_cycle_spec_rejects_incomplete_paper_spec(tmp_path: Path) -> None:
    spec = _spec(tmp_path)
    paper_path = tmp_path / "inputs" / "paper.json"
    _write(paper_path, {"account_id": ACCOUNT_ID})

    with pytest.raises(DailyCycleError, match="missing required fields"):
        validate_cycle_spec(spec, repo_root=tmp_path)


def test_cycle_runs_and_resumes_from_report(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    spec = _spec(tmp_path)
    calls: list[tuple[str, ...]] = []

    def fake_run(command: tuple[str, ...], *, root: Path, log_path: Path, environment: dict[str, str]) -> int:
        calls.append(command)
        return 0

    monkeypatch.setattr(daily_cycle, "_run_command", fake_run)
    first = run_cycle(spec, repo_root=tmp_path)
    assert first["status"] == "passed"
    first_call_count = len(calls)
    assert first_call_count == len(first["stages"])

    second = run_cycle(spec, repo_root=tmp_path)
    assert second["status"] == "passed"
    assert len(calls) == first_call_count
    assert {stage["status"] for stage in second["stages"]} == {"resumed"}


def test_cycle_continues_to_observe_after_a_failed_derived_stage(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    spec = _spec(tmp_path)

    def fake_run(command: tuple[str, ...], *, root: Path, log_path: Path, environment: dict[str, str]) -> int:
        return 1 if command[1] == "feature-batch" else 0

    monkeypatch.setattr(daily_cycle, "_run_command", fake_run)
    report = run_cycle(spec, repo_root=tmp_path)
    status_by_name = {stage["name"]: stage["status"] for stage in report["stages"]}
    assert report["status"] == "attention"
    assert status_by_name["feature-batch"] == "failed"
    assert status_by_name["observe"] == "passed"
    assert status_by_name[f"paper:{ACCOUNT_ID}:run"] == "skipped"
    assert status_by_name["backup"] == "passed"
