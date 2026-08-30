from __future__ import annotations

import json
from copy import deepcopy
from pathlib import Path

import pytest

from research.experiments import (
    BacktestSpecConflictError,
    BacktestSpecError,
    BacktestSpecValidationError,
    build_experiment_spec,
    experiment_sha256,
    input_fingerprint,
    read_experiment_spec,
    write_experiment_spec,
)

SECURITY_A = "10000000-0000-4000-8000-000000000001"
SECURITY_B = "10000000-0000-4000-8000-000000000002"
UNIVERSE_ID = "20000000-0000-4000-8000-000000000001"


def _ref(kind: str, ordinal: int) -> dict[str, str]:
    return {
        "kind": kind,
        "artifact_id": f"30000000-0000-4000-8000-{ordinal:012d}",
        "path": f"inputs/{kind}.json",
        "sha256": f"{ordinal:064x}",
        "available_at": "2025-01-01T00:00:00Z",
        "historical_fitness": "backtest_safe",
    }


def _spec() -> dict:
    return {
        "schema_version": "1.0.0",
        "strategy": {
            "name": "equal_weight",
            "version": "1.0.0",
            "git_commit": "unknown",
            "parameters": {"rebalance_frequency": "monthly"},
        },
        "period": {"start_date": "2025-01-01", "end_date": "2025-01-31"},
        "universe": {
            "universe_id": UNIVERSE_ID,
            "version": "1.0.0",
            "security_ids": [SECURITY_B, SECURITY_A],
            "membership_fingerprint": "a" * 64,
        },
        "inputs": [_ref("membership", 3), _ref("prices", 1), _ref("calendar", 2)],
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
            "initial_cash": "100000",
            "fractional_shares": True,
            "rebalance_frequency": "monthly",
        },
        "cost_policy": {
            "version": "1.0.0",
            "commission_bps": "1",
            "fixed_fee": "0",
            "minimum_fee": "0",
            "spread_bps": "2",
            "slippage_bps": "1",
            "tax_bps": "0",
        },
        "risk_policy": {
            "max_gross_exposure": "1",
            "max_position_weight": "1",
            "max_participation": "1",
        },
        "partitions": [
            {"name": "development", "kind": "development", "start_date": "2025-01-01", "end_date": "2025-01-10"},
            {"name": "validation", "kind": "validation", "start_date": "2025-01-11", "end_date": "2025-01-20"},
            {"name": "holdout", "kind": "holdout", "start_date": "2025-01-21", "end_date": "2025-01-31"},
        ],
        "missing_data_policy": "reject_trade",
    }


def test_experiment_identity_is_deterministic_and_canonical() -> None:
    first = build_experiment_spec(_spec())
    reordered = _spec()
    reordered["inputs"] = list(reversed(reordered["inputs"]))
    reordered["universe"]["security_ids"] = list(reversed(reordered["universe"]["security_ids"]))

    second = build_experiment_spec(reordered)

    assert first == second
    assert first["experiment_id"]
    assert len(experiment_sha256(first)) == 64
    assert len(input_fingerprint(first)) == 64


def test_changed_input_path_or_hash_requires_new_identity() -> None:
    first = build_experiment_spec(_spec())
    changed = deepcopy(_spec())
    changed["inputs"][1]["path"] = "inputs/other-prices.json"
    changed["inputs"][1]["sha256"] = "b" * 64

    second = build_experiment_spec(changed)

    assert second["experiment_id"] != first["experiment_id"]


def test_write_and_read_experiment_is_immutable(tmp_path: Path) -> None:
    spec = build_experiment_spec(_spec())
    path = write_experiment_spec(spec, experiments_root=tmp_path / "experiments")
    loaded = read_experiment_spec(path)

    assert loaded.spec == spec
    assert loaded.sha256 == experiment_sha256(spec)
    assert write_experiment_spec(spec, experiments_root=tmp_path / "experiments") == path

    path.write_bytes(b"tampered\n")
    with pytest.raises(BacktestSpecConflictError):
        write_experiment_spec(spec, experiments_root=tmp_path / "experiments")


@pytest.mark.parametrize(
    ("field", "value", "message"),
    [
        ("inputs", [_ref("prices", 1), _ref("calendar", 2), _ref("prices", 4)], "one artifact per kind"),
        ("missing_data_policy", "forward_fill", "missing_data_policy is unsupported"),
        ("strategy", {"name": "equal_weight", "version": "1.0.0", "git_commit": "unknown", "parameters": {}}, "equal_weight requires"),
    ],
)
def test_invalid_experiment_fields_fail_closed(field: str, value: object, message: str) -> None:
    invalid = _spec()
    invalid[field] = value

    with pytest.raises(BacktestSpecError, match=message):
        build_experiment_spec(invalid)


def test_overlapping_partitions_fail_closed() -> None:
    invalid = _spec()
    invalid["partitions"][1]["start_date"] = "2025-01-10"

    with pytest.raises(BacktestSpecError, match="must not overlap"):
        build_experiment_spec(invalid)


def test_walk_forward_windows_must_be_chronological() -> None:
    invalid = _spec()
    invalid["walk_forward_windows"] = [
        {
            "name": "window-1",
            "train": {"start_date": "2025-01-01", "end_date": "2025-01-10"},
            "calibration": {"start_date": "2025-01-10", "end_date": "2025-01-15"},
            "test": {"start_date": "2025-01-16", "end_date": "2025-01-20"},
        }
    ]

    with pytest.raises(BacktestSpecError, match="chronological"):
        build_experiment_spec(invalid)


def test_unsupported_input_fitness_is_not_admitted() -> None:
    invalid = _spec()
    invalid["inputs"][0]["historical_fitness"] = "installation_replay_only"

    with pytest.raises(BacktestSpecError, match="not admitted as backtest_safe"):
        build_experiment_spec(invalid)


def test_persisted_identity_mismatch_is_a_validation_error(tmp_path: Path) -> None:
    spec = build_experiment_spec(_spec())
    path = write_experiment_spec(spec, experiments_root=tmp_path / "experiments")
    document = spec | {"experiment_id": "40000000-0000-4000-8000-000000000001"}
    path.write_text(json.dumps(document), encoding="utf-8")

    with pytest.raises(BacktestSpecValidationError, match="experiment_id does not match"):
        read_experiment_spec(path)
