"""Evidence-linked integration reports for the v1 research-to-paper workflow."""

from __future__ import annotations

import hashlib
import json
import os
import re
import tempfile
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from uuid import UUID

from .forward_record import ForwardRecordError, validate_forward_record

SCHEMA_VERSION = "1.0.0"
WORKFLOW_VERSION = "python-workflow-1.0.0"
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
_UTC_TIMESTAMP = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$")
_CHECK_ID = re.compile(r"^[a-z][a-z0-9_-]{2,63}$")
_FITNESS = {"backtest_safe", "current_research_only", "installation_replay_only", "unsupported"}


class WorkflowError(ValueError):
    """Base error for workflow specification and report handling."""


class WorkflowConflictError(WorkflowError):
    """Raised when an immutable workflow report path contains different bytes."""


class WorkflowValidationError(WorkflowError):
    """Raised when a workflow input, report, or referenced artifact is invalid."""


@dataclass(frozen=True)
class ValidatedWorkflowReport:
    """A validated report and its path."""

    report_path: Path
    report: dict[str, Any]


def canonical_json(value: Any) -> bytes:
    return json.dumps(
        value,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def sha256_file(path: str | Path) -> str:
    digest = hashlib.sha256()
    try:
        with Path(path).open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise WorkflowValidationError(f"cannot hash workflow artifact {path}: {error}") from error
    return digest.hexdigest()


def _strict_json(path: Path) -> Any:
    def no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate JSON key {key!r}")
            result[key] = value
        return result

    try:
        return json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=no_duplicates,
            parse_constant=lambda constant: (_ for _ in ()).throw(
                ValueError(f"invalid JSON constant {constant}")
            ),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise WorkflowValidationError(f"invalid workflow JSON {path}: {error}") from error


def _exact(value: Any, expected: set[str], *, field: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise WorkflowValidationError(f"{field} must be an object")
    actual = set(value)
    missing = sorted(expected - actual)
    unknown = sorted(actual - expected)
    if missing:
        raise WorkflowValidationError(f"{field} is missing fields: {', '.join(missing)}")
    if unknown:
        raise WorkflowValidationError(f"{field} contains unknown fields: {', '.join(unknown)}")
    return value


def _string(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise WorkflowValidationError(f"{field} must be a non-empty string")
    return value


def _uuid(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _UUID.fullmatch(value):
        raise WorkflowValidationError(f"{field} must be a canonical UUID")
    if str(UUID(value)) != value:
        raise WorkflowValidationError(f"{field} must be a canonical UUID")
    return value


def _sha(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _SHA256.fullmatch(value):
        raise WorkflowValidationError(f"{field} must be a lower-case SHA-256")
    return value


def _timestamp(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _UTC_TIMESTAMP.fullmatch(value):
        raise WorkflowValidationError(f"{field} must be a canonical UTC timestamp")
    try:
        parsed = datetime.fromisoformat(value)
    except ValueError as error:
        raise WorkflowValidationError(f"{field} must be a canonical UTC timestamp") from error
    if parsed.tzinfo is None or parsed.utcoffset() is None or parsed.astimezone(UTC) != parsed:
        raise WorkflowValidationError(f"{field} must be a canonical UTC timestamp")
    return value


def _relative_path(value: Any, *, field: str) -> str:
    path = _string(value, field=field).replace("\\", "/")
    if path.startswith("/") or "\x00" in path:
        raise WorkflowValidationError(f"{field} must be a safe relative path")
    if any(part in {"", ".", ".."} for part in path.split("/")):
        raise WorkflowValidationError(f"{field} must be a safe relative path")
    return path


def _file_ref(value: Any, *, field: str) -> dict[str, str]:
    raw = _exact(value, {"path", "sha256"}, field=field)
    return {
        "path": _relative_path(raw["path"], field=f"{field}.path"),
        "sha256": _sha(raw["sha256"], field=f"{field}.sha256"),
    }


def _resolve_ref(root: Path, value: Mapping[str, str], *, field: str) -> Path:
    path = (root / value["path"]).resolve()
    try:
        path.relative_to(root)
    except ValueError as error:
        raise WorkflowValidationError(f"{field}.path escapes the repository root") from error
    if path.is_symlink() or not path.is_file():
        raise WorkflowValidationError(f"{field}.path must identify a regular file: {value['path']}")
    if sha256_file(path) != value["sha256"]:
        raise WorkflowValidationError(f"{field}.sha256 does not match {value['path']}")
    return path


def validate_workflow_spec(value: Mapping[str, Any], *, repo_root: str | Path) -> dict[str, Any]:
    """Validate a workflow specification and all declared file references."""

    root = Path(repo_root).expanduser().resolve()
    raw = _exact(
        value,
        {
            "$schema",
            "schema_version",
            "workflow_id",
            "scenario",
            "decision_at",
            "research_report",
            "backtest_report",
            "backtest_experiment_ids",
            "paper_report",
            "paper_account_ids",
            "forward_record",
            "commodity_evidence",
            "limitations",
        },
        field="workflow",
    )
    if raw["$schema"] != "../schemas/research-workflow.schema.json":
        raise WorkflowValidationError("workflow.$schema is unsupported")
    if raw["schema_version"] != SCHEMA_VERSION:
        raise WorkflowValidationError("workflow.schema_version is unsupported")
    workflow_id = _uuid(raw["workflow_id"], field="workflow_id")
    scenario = raw["scenario"]
    if scenario not in {"thematic", "cross_market"}:
        raise WorkflowValidationError("scenario must be thematic or cross_market")
    decision_at = _timestamp(raw["decision_at"], field="decision_at")
    research_report = _file_ref(raw["research_report"], field="research_report")
    backtest_report = _file_ref(raw["backtest_report"], field="backtest_report")
    experiment_ids = raw["backtest_experiment_ids"]
    if not isinstance(experiment_ids, list) or len(experiment_ids) < 2:
        raise WorkflowValidationError("backtest_experiment_ids must contain at least two experiments")
    normalized_experiments = [
        _uuid(item, field=f"backtest_experiment_ids[{index}]")
        for index, item in enumerate(experiment_ids)
    ]
    if len(set(normalized_experiments)) != len(normalized_experiments):
        raise WorkflowValidationError("backtest_experiment_ids must be unique")
    paper_report = _file_ref(raw["paper_report"], field="paper_report")
    for field, ref in (
        ("research_report", research_report),
        ("backtest_report", backtest_report),
        ("paper_report", paper_report),
    ):
        _resolve_ref(root, ref, field=field)

    account_ids = raw["paper_account_ids"]
    if not isinstance(account_ids, list) or not account_ids:
        raise WorkflowValidationError("paper_account_ids must be a non-empty array")
    normalized_accounts = [_uuid(item, field=f"paper_account_ids[{index}]") for index, item in enumerate(account_ids)]
    if len(set(normalized_accounts)) != len(normalized_accounts):
        raise WorkflowValidationError("paper_account_ids must be unique")

    forward = _exact(raw["forward_record"], {"status", "evidence"}, field="forward_record")
    forward_status = forward["status"]
    if forward_status not in {"genuine", "recorded_replay", "not_started"}:
        raise WorkflowValidationError("forward_record.status is unsupported")
    forward_evidence = None if forward["evidence"] is None else _file_ref(forward["evidence"], field="forward_record.evidence")
    if forward_evidence is not None:
        evidence_path = _resolve_ref(root, forward_evidence, field="forward_record.evidence")
        if forward_status == "genuine":
            try:
                evidence = _strict_json(evidence_path)
                if not isinstance(evidence, Mapping):
                    raise ForwardRecordError("forward record must be an object")
                validate_forward_record(evidence, repo_root=root)
            except ForwardRecordError as error:
                raise WorkflowValidationError(f"forward_record.evidence is invalid: {error}") from error
    if forward_status == "genuine" and forward_evidence is None:
        raise WorkflowValidationError("genuine forward_record requires evidence")
    commodity = None if raw["commodity_evidence"] is None else _file_ref(raw["commodity_evidence"], field="commodity_evidence")
    if commodity is not None:
        _resolve_ref(root, commodity, field="commodity_evidence")

    limitations = raw["limitations"]
    if not isinstance(limitations, list) or any(not isinstance(item, str) or not item.strip() for item in limitations):
        raise WorkflowValidationError("limitations must be an array of non-empty strings")

    return {
        "$schema": raw["$schema"],
        "schema_version": SCHEMA_VERSION,
        "workflow_id": workflow_id,
        "scenario": scenario,
        "decision_at": decision_at,
        "research_report": research_report,
        "backtest_report": backtest_report,
        "backtest_experiment_ids": normalized_experiments,
        "paper_report": paper_report,
        "paper_account_ids": normalized_accounts,
        "forward_record": {"status": forward_status, "evidence": forward_evidence},
        "commodity_evidence": commodity,
        "limitations": list(limitations),
    }


def load_workflow_spec(path: str | Path, *, repo_root: str | Path) -> dict[str, Any]:
    value = _strict_json(Path(path).expanduser().resolve())
    if not isinstance(value, Mapping):
        raise WorkflowValidationError(f"workflow spec {path} must be an object")
    return validate_workflow_spec(value, repo_root=repo_root)


def _read_ref(root: Path, value: Mapping[str, str], *, field: str) -> dict[str, Any]:
    path = _resolve_ref(root, value, field=field)
    document = _strict_json(path)
    if not isinstance(document, Mapping):
        raise WorkflowValidationError(f"{field} must contain a JSON object")
    return dict(document)


def _check(check_id: str, status: str, observed: str, required: str) -> dict[str, str]:
    if not _CHECK_ID.fullmatch(check_id):
        raise WorkflowValidationError(f"invalid generated check ID {check_id!r}")
    if status not in {"passed", "attention", "failed"}:
        raise WorkflowValidationError(f"invalid generated check status {status!r}")
    return {"check_id": check_id, "status": status, "observed": observed, "required": required}


def _overall_status(checks: Sequence[Mapping[str, Any]]) -> str:
    statuses = {check["status"] for check in checks}
    if "failed" in statuses:
        return "failed"
    if "attention" in statuses:
        return "attention"
    return "passed"


def _add_limitation(limitations: list[str], value: str) -> None:
    if value not in limitations:
        limitations.append(value)


def _id_or_none(value: Any) -> str | None:
    return value if isinstance(value, str) and value else None


def _report_experiments(value: Any) -> list[Mapping[str, Any]]:
    if not isinstance(value, list):
        return []
    return [item for item in value if isinstance(item, Mapping)]


def build_workflow_report(spec: Mapping[str, Any], *, repo_root: str | Path) -> dict[str, Any]:
    """Build a deterministic integration report from previously accepted reports."""

    root = Path(repo_root).expanduser().resolve()
    normalized = validate_workflow_spec(spec, repo_root=root)
    research = _read_ref(root, normalized["research_report"], field="research_report")
    backtest = _read_ref(root, normalized["backtest_report"], field="backtest_report")
    paper = _read_ref(root, normalized["paper_report"], field="paper_report")
    commodity = (
        _read_ref(root, normalized["commodity_evidence"], field="commodity_evidence")
        if normalized["commodity_evidence"] is not None
        else None
    )
    forward_evidence = (
        _read_ref(root, normalized["forward_record"]["evidence"], field="forward_record.evidence")
        if normalized["forward_record"]["evidence"] is not None
        else None
    )

    checks: list[dict[str, str]] = []
    limitations = list(normalized["limitations"])
    fitness: list[dict[str, str]] = []

    research_ids = {
        "theme_id": _id_or_none(research.get("theme_id")),
        "evidence_pack_id": _id_or_none(research.get("evidence_pack_id")),
        "hypothesis_id": _id_or_none(research.get("hypothesis_id")),
        "prediction_id": _id_or_none(research.get("prediction_id")),
    }
    research_chain_complete = all(value is not None for value in research_ids.values())
    checks.append(
        _check(
            "research-chain",
            "passed" if research_chain_complete else "failed",
            ", ".join(f"{key}={value or 'missing'}" for key, value in research_ids.items()),
            "theme, evidence pack, hypothesis, and prediction IDs must be linked",
        )
    )
    checks.append(
        _check(
            "research-cutoff",
            "passed" if research.get("decision_at") == normalized["decision_at"] else "failed",
            f"decision_at={research.get('decision_at', 'missing')}",
            f"decision_at={normalized['decision_at']}",
        )
    )
    future_rejected = research.get("future_reference_rejected") is True
    checks.append(
        _check(
            "future-reference-guard",
            "passed" if future_rejected else "failed",
            f"future_reference_rejected={str(future_rejected).lower()}",
            "future-dated evidence must be rejected",
        )
    )

    reproduction = backtest.get("reproduction")
    reproduction_ok = (
        isinstance(reproduction, Mapping)
        and reproduction.get("manifest_equal") is True
        and reproduction.get("artifact_files_equal") is True
        and reproduction.get("perturbation_requires_new_identity") is True
    )
    checks.append(
        _check(
            "backtest-reproduction",
            "passed" if reproduction_ok and backtest.get("status") == "passed" else "failed",
            f"status={backtest.get('status', 'missing')}; reproduction_equal={str(reproduction_ok).lower()}",
            "a passed backtest reproduction must preserve artifacts and identity boundaries",
        )
    )
    bias = backtest.get("bias_audit")
    bias_ok = isinstance(bias, Mapping) and bias.get("status") == "passed"
    checks.append(
        _check(
            "backtest-bias-audit",
            "passed" if bias_ok else "failed",
            f"bias_audit.status={bias.get('status', 'missing') if isinstance(bias, Mapping) else 'missing'}",
            "the point-in-time bias audit must pass",
        )
    )
    experiments = _report_experiments(backtest.get("experiments"))
    experiment_by_id = {
        str(item["experiment_id"]): item
        for item in experiments
        if isinstance(item.get("experiment_id"), str) and item["experiment_id"]
    }
    selected_experiment_ids = normalized["backtest_experiment_ids"]
    selected_experiments = [experiment_by_id[item] for item in selected_experiment_ids if item in experiment_by_id]
    selection_ok = len(selected_experiments) == len(selected_experiment_ids)
    checks.append(
        _check(
            "backtest-selection",
            "passed" if selection_ok else "failed",
            f"requested={len(selected_experiment_ids)}; present={len(selected_experiments)}",
            "every promoted experiment ID must be present in the backtest report",
        )
    )
    strategy_names = sorted(
        {
            str(item["name"])
            for item in selected_experiments
            if isinstance(item.get("name"), str) and item["name"]
        }
    )
    lowered_names = " ".join(strategy_names).lower()
    baseline_ok = selection_ok and len(selected_experiments) >= 2 and "equal" in lowered_names and "momentum" in lowered_names
    checks.append(
        _check(
            "baseline-comparison",
            "passed" if baseline_ok else "failed",
            f"experiment_count={len(selected_experiments)}; strategies={','.join(strategy_names) or 'missing'}",
            "at least two results must include an equal-weight baseline and a momentum comparison",
        )
    )

    regions = sorted(
        {
            str(item["region"])
            for item in selected_experiments
            if isinstance(item.get("region"), str) and item["region"]
        }
    )
    fixtures = backtest.get("fixtures") if isinstance(backtest.get("fixtures"), Mapping) else {}
    if normalized["scenario"] == "cross_market":
        region_ok = {"US", "BR"}.issubset(regions)
        checks.append(
            _check(
                "cross-market-regions",
                "passed" if region_ok else "failed",
                f"regions={','.join(regions) or 'missing'}",
                "US and BR must both be represented",
            )
        )
        macro_ok = (
            isinstance(fixtures.get("macro_revisions"), int)
            and fixtures["macro_revisions"] >= 1
            and fixtures.get("brazil_reporting_currency") in {"USD", "BRL"}
        )
        checks.append(
            _check(
                "cross-market-clock-and-fx",
                "passed" if macro_ok else "failed",
                f"macro_revisions={fixtures.get('macro_revisions', 'missing')}; brazil_reporting_currency={fixtures.get('brazil_reporting_currency', 'missing')}",
                "the cross-market report must retain macro revision and Brazil FX context",
            )
        )
        commodity_ok = isinstance(commodity, Mapping)
        checks.append(
            _check(
                "commodity-evidence",
                "passed" if commodity_ok else "failed",
                f"declared={str(commodity_ok).lower()}",
                "a cross-market commodity question must declare a source artifact",
            )
        )

    paper_acceptance = paper.get("acceptance") if isinstance(paper.get("acceptance"), Mapping) else {}
    paper_checks = ("forward_sessions", "reconciliation", "rebuild_exact", "duplicate_auto_cycle", "backup_restore")
    paper_ok = paper.get("status") == "passed" and all(paper_acceptance.get(key) is True for key in paper_checks)
    checks.append(
        _check(
            "paper-reconciliation",
            "passed" if paper_ok else "failed",
            f"status={paper.get('status', 'missing')}; required_checks={sum(paper_acceptance.get(key) is True for key in paper_checks)}/{len(paper_checks)}",
            "paper sessions, idempotency, rebuild, backup, and reconciliation must pass",
        )
    )
    paper_accounts = {
        str(item.get("account_id"))
        for item in paper.get("accounts", [])
        if isinstance(item, Mapping) and isinstance(item.get("account_id"), str)
    }
    requested_accounts = set(normalized["paper_account_ids"])
    accounts_ok = requested_accounts.issubset(paper_accounts)
    checks.append(
        _check(
            "paper-account-links",
            "passed" if accounts_ok else "failed",
            f"requested={len(requested_accounts)}; present={len(requested_accounts & paper_accounts)}",
            "every requested paper account must be present in the paper report",
        )
    )

    forward_status = normalized["forward_record"]["status"]
    if forward_status == "genuine":
        genuine_ok = (
            isinstance(forward_evidence, Mapping)
            and forward_evidence.get("wall_clock") is True
            and isinstance(forward_evidence.get("sessions"), int)
            and not isinstance(forward_evidence.get("sessions"), bool)
            and forward_evidence["sessions"] >= 1
            and isinstance(forward_evidence.get("account_ids"), list)
            and requested_accounts.issubset(set(forward_evidence["account_ids"]))
            and forward_evidence.get("fitness") in {"backtest_safe", "current_research_only"}
        )
        checks.append(
            _check(
                "forward-record",
                "passed" if genuine_ok else "failed",
                f"wall_clock={str(forward_evidence.get('wall_clock', False) if isinstance(forward_evidence, Mapping) else False).lower()}; sessions={forward_evidence.get('sessions', 'missing') if isinstance(forward_evidence, Mapping) else 'missing'}",
                "genuine forward evidence must be wall-clock, non-empty, account-linked, and not installation replay",
            )
        )
        if isinstance(forward_evidence, Mapping) and forward_evidence.get("fitness") in _FITNESS:
            fitness.append(
                {
                    "dataset": "paper-forward-record",
                    "classification": forward_evidence["fitness"],
                    "source": normalized["forward_record"]["evidence"]["path"],
                }
            )
    elif forward_status == "recorded_replay":
        checks.append(
            _check(
                "forward-record",
                "attention",
                "status=recorded_replay",
                "v1 entry requires a genuine wall-clock forward paper record",
            )
        )
        fitness.append(
            {
                "dataset": "paper-record",
                "classification": "installation_replay_only",
                "source": normalized["paper_report"]["path"],
            }
        )
        _add_limitation(
            limitations,
            "The linked paper evidence is recorded/replayed and does not satisfy the genuine wall-clock v1 entry criterion.",
        )
    else:
        checks.append(
            _check(
                "forward-record",
                "attention",
                "status=not_started",
                "v1 entry requires a genuine wall-clock forward paper record",
            )
        )
        _add_limitation(limitations, "No genuine wall-clock forward paper record has been supplied yet.")

    if isinstance(commodity, Mapping):
        source = commodity.get("source")
        classification = commodity.get("fitness")
        if isinstance(source, str) and isinstance(classification, str) and classification in _FITNESS:
            fitness.append({"dataset": "commodity-evidence", "classification": classification, "source": source})
            if classification != "backtest_safe":
                _add_limitation(
                    limitations,
                    f"Commodity evidence is labeled {classification}; it must not be treated as backtest-safe historical data.",
                )
                if normalized["scenario"] == "cross_market":
                    checks.append(
                        _check(
                            "commodity-fitness",
                            "attention",
                            f"fitness={classification}",
                            "cross-market conclusions must expose non-backtest-safe commodity evidence",
                        )
                    )
        else:
            checks.append(
                _check(
                    "commodity-fitness",
                    "failed",
                    "source or fitness is missing",
                    "commodity evidence must declare source and data fitness",
                )
            )

    experiment_ids = sorted(selected_experiment_ids)
    report = {
        "schema_version": SCHEMA_VERSION,
        "workflow_id": normalized["workflow_id"],
        "scenario": normalized["scenario"],
        "decision_at": normalized["decision_at"],
        "spec_sha256": sha256_bytes(canonical_json(normalized)),
        "status": _overall_status(checks),
        "forward_record_status": forward_status,
        "evidence": {
            "research_report": normalized["research_report"],
            "backtest_report": normalized["backtest_report"],
            "paper_report": normalized["paper_report"],
            "commodity_evidence": normalized["commodity_evidence"],
        },
        "links": {
            **research_ids,
            "experiment_ids": experiment_ids,
            "paper_account_ids": sorted(requested_accounts),
            "regions": regions,
            "strategies": strategy_names,
        },
        "checks": checks,
        "fitness": fitness,
        "limitations": limitations,
    }
    validate_workflow_report(report, repo_root=root, spec=normalized)
    return report


def validate_workflow_report(
    value: Mapping[str, Any],
    *,
    repo_root: str | Path,
    spec: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    """Validate report structure, referenced hashes, and optional spec identity."""

    root = Path(repo_root).expanduser().resolve()
    raw = _exact(
        value,
        {
            "schema_version",
            "workflow_id",
            "scenario",
            "decision_at",
            "spec_sha256",
            "status",
            "forward_record_status",
            "evidence",
            "links",
            "checks",
            "fitness",
            "limitations",
        },
        field="workflow report",
    )
    if raw["schema_version"] != SCHEMA_VERSION:
        raise WorkflowValidationError("workflow report schema_version is unsupported")
    workflow_id = _uuid(raw["workflow_id"], field="workflow_id")
    if raw["scenario"] not in {"thematic", "cross_market"}:
        raise WorkflowValidationError("workflow report scenario is unsupported")
    decision_at = _timestamp(raw["decision_at"], field="decision_at")
    spec_sha = _sha(raw["spec_sha256"], field="spec_sha256")
    if raw["status"] not in {"passed", "attention", "failed"}:
        raise WorkflowValidationError("workflow report status is unsupported")
    if raw["forward_record_status"] not in {"genuine", "recorded_replay", "not_started"}:
        raise WorkflowValidationError("workflow report forward_record_status is unsupported")

    evidence = _exact(
        raw["evidence"],
        {"research_report", "backtest_report", "paper_report", "commodity_evidence"},
        field="evidence",
    )
    normalized_evidence: dict[str, Any] = {}
    for field in ("research_report", "backtest_report", "paper_report"):
        ref = _file_ref(evidence[field], field=f"evidence.{field}")
        _resolve_ref(root, ref, field=f"evidence.{field}")
        normalized_evidence[field] = ref
    if evidence["commodity_evidence"] is None:
        normalized_evidence["commodity_evidence"] = None
    else:
        ref = _file_ref(evidence["commodity_evidence"], field="evidence.commodity_evidence")
        _resolve_ref(root, ref, field="evidence.commodity_evidence")
        normalized_evidence["commodity_evidence"] = ref

    links = _exact(
        raw["links"],
        {
            "theme_id",
            "evidence_pack_id",
            "hypothesis_id",
            "prediction_id",
            "experiment_ids",
            "paper_account_ids",
            "regions",
            "strategies",
        },
        field="links",
    )
    normalized_links = dict(links)
    for field in ("theme_id", "evidence_pack_id", "hypothesis_id", "prediction_id"):
        if links[field] is not None and (not isinstance(links[field], str) or not links[field]):
            raise WorkflowValidationError(f"links.{field} must be a string or null")
    for field in ("experiment_ids", "paper_account_ids", "regions", "strategies"):
        if not isinstance(links[field], list) or any(not isinstance(item, str) or not item for item in links[field]):
            raise WorkflowValidationError(f"links.{field} must be an array of non-empty strings")

    checks = raw["checks"]
    if not isinstance(checks, list) or not checks:
        raise WorkflowValidationError("checks must be a non-empty array")
    normalized_checks: list[dict[str, str]] = []
    seen_checks: set[str] = set()
    for index, item in enumerate(checks):
        check = _exact(item, {"check_id", "status", "observed", "required"}, field=f"checks[{index}]")
        check_id = _string(check["check_id"], field=f"checks[{index}].check_id")
        if not _CHECK_ID.fullmatch(check_id) or check_id in seen_checks:
            raise WorkflowValidationError(f"checks[{index}].check_id is invalid or duplicated")
        seen_checks.add(check_id)
        status = check["status"]
        if status not in {"passed", "attention", "failed"}:
            raise WorkflowValidationError(f"checks[{index}].status is unsupported")
        normalized_checks.append(
            {
                "check_id": check_id,
                "status": status,
                "observed": _string(check["observed"], field=f"checks[{index}].observed"),
                "required": _string(check["required"], field=f"checks[{index}].required"),
            }
        )
    if raw["status"] != _overall_status(normalized_checks):
        raise WorkflowValidationError("workflow report status does not summarize its checks")

    fitness = raw["fitness"]
    if not isinstance(fitness, list):
        raise WorkflowValidationError("fitness must be an array")
    normalized_fitness: list[dict[str, str]] = []
    for index, item in enumerate(fitness):
        entry = _exact(item, {"dataset", "classification", "source"}, field=f"fitness[{index}]")
        classification = entry["classification"]
        if classification not in _FITNESS:
            raise WorkflowValidationError(f"fitness[{index}].classification is unsupported")
        normalized_fitness.append(
            {
                "dataset": _string(entry["dataset"], field=f"fitness[{index}].dataset"),
                "classification": classification,
                "source": _string(entry["source"], field=f"fitness[{index}].source"),
            }
        )
    limitations = raw["limitations"]
    if not isinstance(limitations, list) or any(not isinstance(item, str) or not item.strip() for item in limitations):
        raise WorkflowValidationError("limitations must be an array of non-empty strings")

    normalized = {
        "schema_version": SCHEMA_VERSION,
        "workflow_id": workflow_id,
        "scenario": raw["scenario"],
        "decision_at": decision_at,
        "spec_sha256": spec_sha,
        "status": raw["status"],
        "forward_record_status": raw["forward_record_status"],
        "evidence": normalized_evidence,
        "links": normalized_links,
        "checks": normalized_checks,
        "fitness": normalized_fitness,
        "limitations": list(limitations),
    }
    if spec is not None:
        normalized_spec = validate_workflow_spec(spec, repo_root=root)
        if normalized["spec_sha256"] != sha256_bytes(canonical_json(normalized_spec)):
            raise WorkflowValidationError("workflow report does not belong to the supplied specification")
        if (
            normalized["workflow_id"] != normalized_spec["workflow_id"]
            or normalized["scenario"] != normalized_spec["scenario"]
            or normalized["decision_at"] != normalized_spec["decision_at"]
            or normalized["forward_record_status"] != normalized_spec["forward_record"]["status"]
        ):
            raise WorkflowValidationError("workflow report identity does not match its specification")
        expected_evidence = {
            "research_report": normalized_spec["research_report"],
            "backtest_report": normalized_spec["backtest_report"],
            "paper_report": normalized_spec["paper_report"],
            "commodity_evidence": normalized_spec["commodity_evidence"],
        }
        if normalized["evidence"] != expected_evidence:
            raise WorkflowValidationError("workflow report evidence does not match its specification")
        if normalized["links"]["experiment_ids"] != sorted(normalized_spec["backtest_experiment_ids"]):
            raise WorkflowValidationError("workflow report experiment links do not match its specification")
    return normalized


def read_workflow_report(
    path: str | Path,
    *,
    repo_root: str | Path,
    spec: Mapping[str, Any] | None = None,
) -> ValidatedWorkflowReport:
    report_path = Path(path).expanduser().resolve()
    value = _strict_json(report_path)
    if not isinstance(value, Mapping):
        raise WorkflowValidationError(f"workflow report {path} must be an object")
    return ValidatedWorkflowReport(
        report_path=report_path,
        report=validate_workflow_report(value, repo_root=repo_root, spec=spec),
    )


def write_workflow_report(report: Mapping[str, Any], *, path: str | Path, repo_root: str | Path) -> Path:
    """Write an immutable report, allowing an identical existing report."""

    output = Path(path).expanduser().resolve()
    normalized = validate_workflow_report(report, repo_root=repo_root)
    content = canonical_json(normalized) + b"\n"
    output.parent.mkdir(parents=True, exist_ok=True)
    if output.exists():
        if not output.is_file() or output.read_bytes() != content:
            raise WorkflowConflictError(f"workflow report path conflicts: {output}")
        return output
    temporary: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(dir=output.parent, prefix=f".{output.name}.", delete=False) as stream:
            temporary = Path(stream.name)
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        try:
            os.link(temporary, output)
        except FileExistsError:
            if not output.is_file() or output.read_bytes() != content:
                raise WorkflowConflictError(f"workflow report appeared with different bytes: {output}")
        return output
    except OSError as error:
        raise WorkflowError(f"cannot write workflow report {output}: {error}") from error
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


__all__ = [
    "SCHEMA_VERSION",
    "WORKFLOW_VERSION",
    "ValidatedWorkflowReport",
    "WorkflowConflictError",
    "WorkflowError",
    "WorkflowValidationError",
    "build_workflow_report",
    "canonical_json",
    "load_workflow_spec",
    "read_workflow_report",
    "sha256_bytes",
    "sha256_file",
    "validate_workflow_report",
    "validate_workflow_spec",
    "write_workflow_report",
]
