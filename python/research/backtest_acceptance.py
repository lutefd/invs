"""Retained bounded v0.5 baseline and bias acceptance drill."""

from __future__ import annotations

import argparse
import hashlib
import json
from decimal import Decimal
from pathlib import Path
from typing import Any

from .backtest import BacktestInterruptedError, simulate_backtest
from .backtest_results import publish_backtest_result, read_backtest_result
from .bias_audit import publish_bias_audit, validate_bias_audit
from .experiments import build_experiment_spec, canonical_json, write_experiment_spec

AVAILABLE_AT = "2025-01-01T00:01:00Z"
OBSERVED_AT = "2025-01-01T00:00:00Z"
PERIOD = {"start_date": "2025-01-02", "end_date": "2025-01-23"}
DATES = (
    "2025-01-02",
    "2025-01-03",
    "2025-01-06",
    "2025-01-07",
    "2025-01-08",
    "2025-01-09",
    "2025-01-10",
    "2025-01-13",
    "2025-01-14",
    "2025-01-15",
    "2025-01-16",
    "2025-01-17",
    "2025-01-21",
    "2025-01-22",
    "2025-01-23",
)
US_A = "10000000-0000-4000-8000-000000000011"
US_B = "10000000-0000-4000-8000-000000000012"
BR_A = "10000000-0000-4000-8000-000000000021"
BR_B = "10000000-0000-4000-8000-000000000022"
US_UNIVERSE = "20000000-0000-4000-8000-000000000011"
BR_UNIVERSE = "20000000-0000-4000-8000-000000000021"


def _artifact_id(region: str, ordinal: int) -> str:
    region_offset = 100 if region == "US" else 200
    return f"30000000-0000-4000-8000-{region_offset + ordinal:012d}"


def _write_immutable(path: Path, document: Any) -> bytes:
    content = canonical_json(document) + b"\n"
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        if path.read_bytes() != content:
            raise RuntimeError(f"immutable acceptance path conflicts: {path}")
        return content
    path.write_bytes(content)
    return content


def _write_artifact(
    root: Path,
    *,
    region: str,
    kind: str,
    ordinal: int,
    rows: list[dict[str, Any]],
    available_at: str = AVAILABLE_AT,
) -> dict[str, Any]:
    artifact_id = _artifact_id(region, ordinal)
    document = {
        "schema_version": "1.0.0",
        "artifact_kind": kind,
        "artifact_id": artifact_id,
        "available_at": available_at,
        "rows": rows,
    }
    relative_path = Path("inputs") / region.lower() / f"{kind}.json"
    path = root / relative_path
    content = _write_immutable(path, document)
    return {
        "kind": kind,
        "artifact_id": artifact_id,
        "path": relative_path.as_posix(),
        "sha256": hashlib.sha256(content).hexdigest(),
        "available_at": available_at,
        "historical_fitness": "backtest_safe",
    }


def _price(security_id: str, session_date: str, open_price: str, close: str, currency: str) -> dict[str, Any]:
    open_decimal = Decimal(open_price)
    close_decimal = Decimal(close)
    return {
        "security_id": security_id,
        "session_date": session_date,
        "observed_at": OBSERVED_AT,
        "available_at": AVAILABLE_AT,
        "currency": currency,
        "price_basis": "raw",
        "open": open_price,
        "high": str(max(open_decimal, close_decimal)),
        "low": str(min(open_decimal, close_decimal)),
        "close": close,
        "volume": "1000000",
        "has_volume": True,
    }


def _calendar(region: str) -> list[dict[str, Any]]:
    open_time = "14:30:00Z" if region == "US" else "13:00:00Z"
    close_time = "21:00:00Z" if region == "US" else "20:00:00Z"
    return [
        {
            "session_date": session_date,
            "open_at": f"{session_date}T{open_time}",
            "close_at": f"{session_date}T{close_time}",
            "available_at": AVAILABLE_AT,
        }
        for session_date in DATES
    ]


def _prices(region: str, *, perturb: bool = False) -> list[dict[str, Any]]:
    first, second = (US_A, US_B) if region == "US" else (BR_A, BR_B)
    currency = "USD" if region == "US" else "BRL"
    rows: list[dict[str, Any]] = []
    for index, session_date in enumerate(DATES):
        if region == "US":
            if index < 6:
                first_close = Decimal(100 + index * 2)
            else:
                first_close = Decimal(56 + (index - 6) * 2)
            second_close = Decimal(80 + index * 2)
            first_open = first_close - Decimal(1)
            second_open = second_close - Decimal(1)
        else:
            first_close = Decimal(500 + index * 4)
            second_close = Decimal(300 + index * 3)
            first_open = first_close - Decimal(2)
            second_open = second_close - Decimal(2)
        if perturb and index == 4:
            first_close += Decimal(1)
        rows.extend(
            [
                _price(first, session_date, str(first_open), str(first_close), currency),
                _price(second, session_date, str(second_open), str(second_close), currency),
            ]
        )
    return rows


def _membership(first: str, second: str) -> list[dict[str, Any]]:
    return [
        {
            "security_id": security_id,
            "valid_from": "2025-01-01",
            "valid_until": "2025-01-17",
            "member": True,
            "available_at": AVAILABLE_AT,
            "revision": 0,
        }
        for security_id in (first, second)
    ] + [
        {
            "security_id": second,
            "valid_from": "2025-01-17",
            "valid_until": None,
            "member": False,
            "available_at": AVAILABLE_AT,
            "revision": 0,
        }
    ] + [
        {
            "security_id": first,
            "valid_from": "2025-01-17",
            "valid_until": None,
            "member": True,
            "available_at": AVAILABLE_AT,
            "revision": 0,
        }
    ]


def _actions(region: str) -> list[dict[str, Any]]:
    first, second = (US_A, US_B) if region == "US" else (BR_A, BR_B)
    currency = "USD" if region == "US" else "BRL"
    actions = [
        {
            "id": _artifact_id(region, 10),
            "security_id": first,
            "action_type": "cash_dividend",
            "effective_date": "2025-01-09",
            "payment_date": "2025-01-10",
            "available_at": AVAILABLE_AT,
            "currency": currency,
            "ratio_numerator": "1",
            "ratio_denominator": "1",
            "cash_amount": "1",
            "settlement_price": None,
            "target_security_id": None,
        }
    ]
    if region == "US":
        actions.extend(
            [
                {
                    "id": _artifact_id(region, 11),
                    "security_id": first,
                    "action_type": "split",
                    "effective_date": "2025-01-10",
                    "payment_date": None,
                    "available_at": AVAILABLE_AT,
                    "currency": currency,
                    "ratio_numerator": "2",
                    "ratio_denominator": "1",
                    "cash_amount": "0",
                    "settlement_price": None,
                    "target_security_id": None,
                },
                {
                    "id": _artifact_id(region, 12),
                    "security_id": second,
                    "action_type": "delisting",
                    "effective_date": "2025-01-17",
                    "payment_date": None,
                    "available_at": AVAILABLE_AT,
                    "currency": currency,
                    "ratio_numerator": "1",
                    "ratio_denominator": "1",
                    "cash_amount": "0",
                    "settlement_price": "102",
                    "target_security_id": None,
                },
            ]
        )
    return actions


def _macro(region: str) -> list[dict[str, Any]]:
    return [
        {
            "observation_id": f"{region.lower()}-macro-growth",
            "observed_at": "2025-01-09T00:00:00Z",
            "available_at": "2025-01-10T00:01:00Z",
            "value": "1.0",
            "revision": 0,
        },
        {
            "observation_id": f"{region.lower()}-macro-growth",
            "observed_at": "2025-01-09T00:00:00Z",
            "available_at": "2025-01-16T00:01:00Z",
            "value": "1.1",
            "revision": 1,
        },
    ]


def _fx() -> list[dict[str, Any]]:
    return [
        {
            "fixing_at": "2025-01-01T00:00:00Z",
            "base_currency": "USD",
            "quote_currency": "BRL",
            "rate": "5",
            "available_at": AVAILABLE_AT,
        },
        {
            "fixing_at": "2025-01-15T00:00:00Z",
            "base_currency": "USD",
            "quote_currency": "BRL",
            "rate": "5.1",
            "available_at": "2025-01-15T00:01:00Z",
        },
    ]


def _fixture(root: Path, region: str, *, perturb: bool = False) -> list[dict[str, Any]]:
    first, second = (US_A, US_B) if region == "US" else (BR_A, BR_B)
    refs = [
        _write_artifact(
            root,
            region=region,
            kind="prices",
            ordinal=1,
            rows=_prices(region, perturb=perturb),
        ),
        _write_artifact(root, region=region, kind="calendar", ordinal=2, rows=_calendar(region)),
        _write_artifact(
            root,
            region=region,
            kind="membership",
            ordinal=3,
            rows=_membership(first, second),
        ),
        _write_artifact(
            root,
            region=region,
            kind="corporate_actions",
            ordinal=4,
            rows=_actions(region),
        ),
        _write_artifact(
            root,
            region=region,
            kind="macro",
            ordinal=5,
            rows=_macro(region),
            available_at="2025-01-16T00:01:00Z",
        ),
    ]
    if region == "BR":
        refs.append(
            _write_artifact(
                root,
                region=region,
                kind="fx",
                ordinal=6,
                rows=_fx(),
                available_at="2025-01-15T00:01:00Z",
            )
        )
    return refs


def _cost_policy(region: str, conservative: bool) -> dict[str, str]:
    if not conservative:
        version = "1.0.0"
        values = {key: "0" for key in ("commission_bps", "fixed_fee", "minimum_fee", "spread_bps", "slippage_bps", "tax_bps")}
    elif region == "US":
        version = "1.1.0"
        values = {
            "commission_bps": "5",
            "fixed_fee": "1",
            "minimum_fee": "1",
            "spread_bps": "4",
            "slippage_bps": "3",
            "tax_bps": "0",
        }
    else:
        version = "2.0.0"
        values = {
            "commission_bps": "10",
            "fixed_fee": "2",
            "minimum_fee": "2",
            "spread_bps": "8",
            "slippage_bps": "5",
            "tax_bps": "20",
        }
    return {"version": version, **values}


def _spec(region: str, refs: list[dict[str, Any]], strategy: str, conservative: bool) -> dict[str, Any]:
    first, _ = (US_A, US_B) if region == "US" else (BR_A, BR_B)
    currency = "USD" if region == "US" else "BRL"
    parameters = {} if strategy == "buy_and_hold" else {
        "rebalance_frequency": "daily"
    }
    if strategy == "momentum_12_1":
        parameters = {
            "lookback_sessions": 4,
            "skip_sessions": 1,
            "top_k": 1,
            "rebalance_frequency": "daily",
        }
    return build_experiment_spec(
        {
            "schema_version": "1.0.0",
            "strategy": {
                "name": strategy,
                "version": "1.0.0",
                "git_commit": "unknown",
                "parameters": parameters,
            },
            "period": PERIOD,
            "universe": {
                "universe_id": US_UNIVERSE if region == "US" else BR_UNIVERSE,
                "version": "1.0.0",
                "security_ids": sorted((US_A, US_B) if region == "US" else (BR_A, BR_B)),
                "membership_fingerprint": ("a" if region == "US" else "b") * 64,
            },
            "inputs": refs,
            "benchmark": {"security_id": first, "currency": currency},
            "decision_policy": {
                "name": "after_close_next_session_open",
                "frequency": "daily",
                "signal_delay_sessions": 1,
                "execution_price": "open",
            },
            "accounting_policy": {
                "base_currency": currency,
                "reporting_currency": None if region == "US" else "USD",
                "initial_cash": "100000",
                "fractional_shares": True,
                "rebalance_frequency": "daily",
            },
            "cost_policy": _cost_policy(region, conservative),
            "risk_policy": {
                "max_gross_exposure": "1",
                "max_position_weight": "1",
                "max_participation": "1",
            },
            "metrics_policy": {
                "version": "1.0.0",
                "return_basis": "close_to_close",
                "annualization_factor": 252,
                "risk_free_source": "constant_annual",
                "risk_free_annual": "0",
                "missing_period_policy": "reject",
            },
            "partitions": [
                {"name": "development", "kind": "development", "start_date": "2025-01-02", "end_date": "2025-01-09"},
                {"name": "validation", "kind": "validation", "start_date": "2025-01-10", "end_date": "2025-01-16"},
                {"name": "holdout", "kind": "holdout", "start_date": "2025-01-17", "end_date": "2025-01-23"},
            ],
            "missing_data_policy": "reject_trade",
        }
    )


def _publish(spec: dict[str, Any], *, workspace: Path, results_root: Path) -> Path:
    write_experiment_spec(spec, experiments_root=workspace / "experiments")
    run = simulate_backtest(spec, data_root=workspace)
    manifest = publish_backtest_result(run, results_root=results_root)
    read_backtest_result(manifest)
    return manifest


def _assert_equal_files(first: Path, second: Path) -> None:
    for name in ("manifest.json", "nav.json", "holdings.json", "orders.json", "fills.json", "ledger.json", "metrics.json"):
        if (first / name).read_bytes() != (second / name).read_bytes():
            raise RuntimeError(f"clean replay differs in {name}")


def _bias_spec(root: Path) -> tuple[Path, Path]:
    evidence_path = root / "bias" / "evidence.json"
    evidence = _write_immutable(evidence_path, {"source": "v0.5-bounded-fixture"})
    evidence_hash = hashlib.sha256(evidence).hexdigest()
    us_categories = (
        "identity",
        "membership",
        "calendar",
        "price",
        "macro_vintage",
        "corporate_action",
        "filing",
    )
    br_categories = ("identity", "membership", "calendar", "price", "corporate_action", "fx")
    datasets: list[dict[str, str]] = []
    probes: list[dict[str, Any]] = []
    boundary = "2026-01-02T03:04:05.000001Z"
    before = "2026-01-02T03:04:05Z"
    after = "2026-01-02T03:04:05.000002Z"
    for region, categories in (("US", us_categories), ("BR", br_categories)):
        for category in categories:
            dataset_id = f"{region.lower()}-{category}"
            kind = "availability_transition"
            classification = "backtest_safe"
            states = ((False, "absent"), (True, "eligible"), (True, "eligible"))
            if category == "membership":
                kind = "removal_transition"
                states = ((True, "eligible"), (False, "absent"), (False, "absent"))
            elif (region, category) in {("US", "macro_vintage"), ("BR", "calendar")}:
                kind = "revision_transition"
                states = ((True, "prior_revision"), (True, "eligible"), (True, "eligible"))
            elif region == "BR" and category == "price":
                kind = "blocked_before_receipt"
                classification = "installation_replay_only"
            elif region == "BR" and category == "corporate_action":
                kind = "unsupported_blocks"
                classification = "installation_replay_only"
                states = ((False, "absent"), (False, "unsupported"), (False, "unsupported"))
            datasets.append(
                {
                    "id": dataset_id,
                    "classification": classification,
                    "availability_policy": "exact fixture availability",
                    "scope_decision": "bounded fixture only",
                }
            )
            probes.append(
                {
                    "id": f"{dataset_id}-boundary",
                    "region": region,
                    "category": category,
                    "dataset_id": dataset_id,
                    "kind": kind,
                    "boundary_at": boundary,
                    "before": {"decision_at": before, "eligible": states[0][0], "state": states[0][1]},
                    "at": {"decision_at": boundary, "eligible": states[1][0], "state": states[1][1]},
                    "after": {"decision_at": after, "eligible": states[2][0], "state": states[2][1]},
                    "evidence_artifact_ids": ["fixture-evidence"],
                }
            )
    spec = {
        "schema_version": "1.0.0",
        "audit_id": "v05-bounded-us-br-fixture",
        "git_commit": "a" * 40,
        "datasets": datasets,
        "artifacts": [{"id": "fixture-evidence", "path": evidence_path.name, "sha256": evidence_hash}],
        "probes": probes,
    }
    path = root / "bias" / "spec.json"
    _write_immutable(path, spec)
    return path, evidence_path


def run_acceptance(acceptance_root: str | Path) -> Path:
    root = Path(acceptance_root).expanduser().resolve()
    clean_root = root / "workspaces" / "clean"
    replay_root = root / "workspaces" / "replay"
    perturbed_root = root / "workspaces" / "perturbed"
    results_root = root / "results"
    us_refs = _fixture(clean_root, "US")
    br_refs = _fixture(clean_root, "BR")
    _fixture(replay_root, "US")
    perturbed_us_refs = _fixture(perturbed_root, "US", perturb=True)

    specifications: dict[str, dict[str, Any]] = {}
    manifests: dict[str, Path] = {}
    for key, region, refs, strategy, conservative in (
        ("us-buy-and-hold-zero", "US", us_refs, "buy_and_hold", False),
        ("us-equal-weight-zero", "US", us_refs, "equal_weight", False),
        ("us-momentum-zero", "US", us_refs, "momentum_12_1", False),
        ("us-equal-weight-conservative", "US", us_refs, "equal_weight", True),
        ("br-equal-weight-conservative", "BR", br_refs, "equal_weight", True),
    ):
        spec = _spec(region, refs, strategy, conservative)
        specifications[key] = spec
        manifests[key] = _publish(spec, workspace=clean_root, results_root=results_root)

    replay_spec = _spec("US", _fixture(replay_root, "US"), "equal_weight", False)
    replay_manifest = _publish(replay_spec, workspace=replay_root, results_root=results_root)
    clean_result = read_backtest_result(manifests["us-equal-weight-zero"])
    replay_result = read_backtest_result(replay_manifest)
    if clean_result.manifest != replay_result.manifest:
        raise RuntimeError("clean replay manifest differs")
    _assert_equal_files(manifests["us-equal-weight-zero"].parent, replay_manifest.parent)

    changed_spec = _spec("US", perturbed_us_refs, "equal_weight", False)
    if changed_spec["experiment_id"] == specifications["us-equal-weight-zero"]["experiment_id"]:
        raise RuntimeError("perturbed input retained the original experiment identity")

    zero_result = read_backtest_result(manifests["us-equal-weight-zero"])
    conservative_result = read_backtest_result(manifests["us-equal-weight-conservative"])
    zero_metrics = zero_result.manifest["metrics"]["values"]
    conservative_metrics = conservative_result.manifest["metrics"]["values"]
    if Decimal(conservative_metrics["total_cost"]) <= 0:
        raise RuntimeError("conservative US costs were not charged")
    if conservative_metrics["total_return"] == zero_metrics["total_return"]:
        raise RuntimeError("conservative US costs did not affect return")
    if not any(
        row["decision_session"] == "2025-01-17" and row["execution_session"] == "2025-01-21"
        for row in zero_result.artifacts["orders"]
    ):
        raise RuntimeError("US order did not skip the 2025-01-20 holiday")
    event_types = {row["event_type"] for row in zero_result.artifacts["ledger"]}
    if not {"split", "dividend", "delisting"}.issubset(event_types):
        raise RuntimeError("US action ledger is incomplete")
    if not any(
        row["reason"] == "membership_exit" and row["execution_session"] == "2025-01-21"
        for row in read_backtest_result(manifests["br-equal-weight-conservative"]).artifacts["orders"]
    ):
        raise RuntimeError("Brazil membership removal did not produce a delayed exit")

    macro_path = clean_root / "inputs" / "us" / "macro.json"
    macro_document = json.loads(macro_path.read_text(encoding="utf-8"))
    if len(macro_document["rows"]) != 2 or {row["revision"] for row in macro_document["rows"]} != {0, 1}:
        raise RuntimeError("macro revision fixture is incomplete")

    bias_spec, _ = _bias_spec(root)
    bias_manifest = publish_bias_audit(bias_spec, audits_root=root / "bias" / "artifacts")
    validated_bias = validate_bias_audit(bias_manifest)

    def relative(path: Path) -> str:
        return path.relative_to(root).as_posix()

    report = {
        "schema_version": "1.0.0",
        "status": "passed",
        "regions": ["BR", "US"],
        "baseline_count": 5,
        "experiments": [
            {
                "name": key,
                "region": "BR" if key.startswith("br-") else "US",
                "experiment_id": specifications[key]["experiment_id"],
                "result_id": read_backtest_result(path).manifest["result_id"],
                "manifest_path": relative(path),
                "total_return": read_backtest_result(path).manifest["metrics"]["values"]["total_return"],
                "total_cost": read_backtest_result(path).manifest["metrics"]["values"]["total_cost"],
            }
            for key, path in manifests.items()
        ],
        "reproduction": {
            "clean_manifest": relative(manifests["us-equal-weight-zero"]),
            "replay_manifest": relative(replay_manifest),
            "manifest_equal": True,
            "artifact_files_equal": True,
            "perturbed_experiment_id": changed_spec["experiment_id"],
            "perturbation_requires_new_identity": True,
        },
        "fixtures": {
            "holiday": "2025-01-20 omitted; 2025-01-17 decision executed 2025-01-21",
            "us_action_events": sorted(event_types),
            "macro_revisions": 2,
            "brazil_reporting_currency": "USD",
        },
        "bias_audit": {
            "status": validated_bias.manifest["status"],
            "artifact_id": validated_bias.manifest["artifact_id"],
            "manifest_path": relative(bias_manifest),
            "probe_count": validated_bias.manifest["probe_count"],
        },
    }
    report_path = root / "backtest-reproduction.json"
    _write_immutable(report_path, report)
    return report_path


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run the retained v0.5 baseline acceptance drill.")
    parser.add_argument("--acceptance-root", required=True)
    args = parser.parse_args(argv)
    try:
        report = run_acceptance(args.acceptance_root)
    except (BacktestInterruptedError, OSError, RuntimeError, TypeError, ValueError) as error:
        print(f"backtest acceptance failed: {error}")
        return 1
    print(json.dumps({"status": "passed", "report_path": str(report)}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
