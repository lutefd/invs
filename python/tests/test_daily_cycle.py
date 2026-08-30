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
        "decision_at": "2026-08-29T21:05:00Z",
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
    paper_run = next(
        stage
        for stage in plan
        if stage.name == "paper:40000000-0000-4000-8000-000000000001:run"
    )
    assert "PAPER_DECISION_AT=2026-08-29T21:05:00Z" in paper_run.command


def test_cycle_spec_rejects_noncanonical_decision_timestamp(tmp_path: Path) -> None:
    spec = _spec(tmp_path)
    spec["decision_at"] = "2026-08-29T21:05:00+00:00"

    with pytest.raises(DailyCycleError, match="decision_at must be a canonical UTC timestamp"):
        validate_cycle_spec(spec, repo_root=tmp_path)


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


def test_cycle_spec_rejects_symlinked_input_file(tmp_path: Path) -> None:
    spec = _spec(tmp_path)
    target = tmp_path / "inputs" / "calendar.json"
    link = tmp_path / "inputs" / "calendar-link.json"
    link.symlink_to(target)
    spec["feature"]["calendar_pin"] = "inputs/calendar-link.json"

    with pytest.raises(DailyCycleError, match="must not traverse symlinks"):
        validate_cycle_spec(spec, repo_root=tmp_path)


def test_cycle_spec_rejects_symlinked_ledger_path(tmp_path: Path) -> None:
    data_root = tmp_path / "data"
    data_root.mkdir()
    outside = tmp_path / "outside-ledger"
    outside.mkdir()
    (data_root / "ledger-link").symlink_to(outside, target_is_directory=True)
    spec = _spec(tmp_path)
    spec["ledger_root"] = "ledger-link"

    with pytest.raises(DailyCycleError, match="ledger_root must not traverse symlinks"):
        validate_cycle_spec(spec, repo_root=tmp_path)

    assert list(outside.iterdir()) == []


def test_cycle_spec_rejects_symlinked_log_directory(tmp_path: Path) -> None:
    outside = tmp_path / "outside-logs"
    outside.mkdir()
    (tmp_path / "logs-link").symlink_to(outside, target_is_directory=True)
    spec = _spec(tmp_path)
    spec["log_dir"] = "logs-link"

    with pytest.raises(DailyCycleError, match="log_dir must not traverse symlinks"):
        validate_cycle_spec(spec, repo_root=tmp_path)

    assert list(outside.iterdir()) == []


def test_cycle_rejects_symlinked_report_path(tmp_path: Path) -> None:
    spec = _spec(tmp_path)
    report_path = tmp_path / ".runtime" / "cycles" / "test.json"
    report_path.parent.mkdir(parents=True)
    report_path.symlink_to(tmp_path / "report-target.json")

    with pytest.raises(DailyCycleError, match="report_path must not traverse symlinks"):
        run_cycle(spec, repo_root=tmp_path)


def test_cycle_rejects_existing_report_temporary_symlink(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    report_path = tmp_path / ".runtime" / "cycles" / "test.json"
    report_path.parent.mkdir(parents=True)
    temporary = report_path.with_name(f".{report_path.name}.4242.tmp")
    target = tmp_path / "outside-report.json"
    temporary.symlink_to(target)
    monkeypatch.setattr(daily_cycle.os, "getpid", lambda: 4242)

    with pytest.raises(DailyCycleError, match="cannot write cycle report"):
        daily_cycle._write_report(report_path, {"status": "test"})

    assert temporary.is_symlink()
    assert not target.exists()


def test_cycle_rejects_symlinked_stage_log_path(tmp_path: Path) -> None:
    outside = tmp_path / "outside-logs"
    outside.mkdir()
    log_dir = tmp_path / "logs-link"
    log_dir.symlink_to(outside, target_is_directory=True)

    with pytest.raises(DailyCycleError, match="stage log path must not traverse symlinks"):
        daily_cycle._run_command(
            ("true",),
            root=tmp_path,
            log_path=log_dir / "01-stage.log",
            environment=dict(os.environ),
        )

    assert list(outside.iterdir()) == []


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


@pytest.mark.parametrize("relative_path", ["inputs/calendar.json", "inputs/paper.json"])
def test_cycle_rejects_changed_referenced_input_on_resume(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, relative_path: str
) -> None:
    spec = _spec(tmp_path)

    def fake_run(command: tuple[str, ...], *, root: Path, log_path: Path, environment: dict[str, str]) -> int:
        return 0

    monkeypatch.setattr(daily_cycle, "_run_command", fake_run)
    first = run_cycle(spec, repo_root=tmp_path)
    assert first["status"] == "passed"

    path = tmp_path / relative_path
    path.write_bytes(path.read_bytes() + b"\n")

    with pytest.raises(DailyCycleError, match="different cycle specification"):
        run_cycle(spec, repo_root=tmp_path)


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
