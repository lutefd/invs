#!/usr/bin/env python3
"""Run one explicit, resumable local collect-to-paper cycle."""

from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
import re
import subprocess
import sys
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, date, datetime
from pathlib import Path
from typing import Any
from uuid import UUID

SCHEMA_VERSION = "1.0.0"
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_CYCLE_ID = re.compile(r"^[a-z0-9][a-z0-9._-]{2,127}$")
_SOURCE = re.compile(r"^[a-z0-9_-]+$")
_RUN_KEY = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$")
_UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
_SEMVER = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
_PAPER_SCHEMA_VERSION = "1.0.0"
_PAPER_ACCOUNT_REQUIRED_FIELDS = frozenset(
    {
        "schema_version",
        "account_id",
        "name",
        "strategy",
        "period",
        "universe",
        "security_metadata",
        "inputs",
        "benchmark",
        "decision_policy",
        "accounting_policy",
        "cost_policy",
        "risk_policy",
        "approval_policy",
        "missing_data_policy",
    }
)
_PAPER_ACCOUNT_OPTIONAL_FIELDS = frozenset({"promoted_backtest"})


class DailyCycleError(ValueError):
    """Raised when a cycle specification or report is unsafe or malformed."""


@dataclass(frozen=True)
class Stage:
    name: str
    command: tuple[str, ...]
    dependencies: tuple[str, ...] = ()


def _strict_json(path: Path) -> dict[str, Any]:
    def no_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate JSON key {key!r}")
            result[key] = value
        return result

    try:
        value = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=no_duplicates,
            parse_constant=lambda constant: (_ for _ in ()).throw(
                ValueError(f"invalid JSON constant {constant}")
            ),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise DailyCycleError(f"invalid cycle JSON {path}: {error}") from error
    if not isinstance(value, dict):
        raise DailyCycleError(f"cycle JSON {path} must be an object")
    return value


def _exact(value: Any, expected: set[str], *, field: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise DailyCycleError(f"{field} must be an object")
    actual = set(value)
    missing = sorted(expected - actual)
    unknown = sorted(actual - expected)
    if missing:
        raise DailyCycleError(f"{field} is missing fields: {', '.join(missing)}")
    if unknown:
        raise DailyCycleError(f"{field} contains unknown fields: {', '.join(unknown)}")
    return value


def _string(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value:
        raise DailyCycleError(f"{field} must be a non-empty string")
    return value


def _relative_path(value: Any, *, field: str) -> str:
    value = _string(value, field=field).replace("\\", "/")
    if value.startswith("/") or "\x00" in value:
        raise DailyCycleError(f"{field} must be a safe relative path")
    if any(part == ".." for part in value.split("/")):
        raise DailyCycleError(f"{field} must not contain parent traversal")
    return value


def _safe_repo_path(repo_root: Path, relative: str, *, field: str) -> Path:
    declared = repo_root / relative
    current = repo_root
    try:
        parts = declared.relative_to(repo_root).parts
    except ValueError as error:
        raise DailyCycleError(f"{field} escapes the repository root") from error
    for part in parts:
        current /= part
        if current.is_symlink():
            raise DailyCycleError(f"{field} must not traverse symlinks: {relative}")
    resolved = declared.resolve()
    try:
        resolved.relative_to(repo_root)
    except ValueError as error:
        raise DailyCycleError(f"{field} escapes the repository root") from error
    return resolved


def _repo_file(repo_root: Path, value: Any, *, field: str) -> Path:
    relative = _relative_path(value, field=field)
    path = _safe_repo_path(repo_root, relative, field=field)
    if not path.is_file():
        raise DailyCycleError(f"{field} must identify a regular file: {relative}")
    return path


def _sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise DailyCycleError(f"cannot hash cycle input {path}: {error}") from error
    return digest.hexdigest()


def _timestamp() -> str:
    return datetime.now(UTC).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def _validate_backup_path(value: Any, repo_root: Path) -> str:
    raw = _string(value, field="backup_dir")
    path = Path(raw).expanduser()
    if not path.is_absolute() or "\x00" in raw:
        raise DailyCycleError("backup_dir must be an absolute path outside the checkout")
    resolved = path.resolve()
    if resolved in {Path("/"), Path("/home"), Path("/tmp")}:
        raise DailyCycleError("backup_dir is too broad to be a backup destination")
    try:
        resolved.relative_to(repo_root)
    except ValueError:
        return str(resolved)
    raise DailyCycleError("backup_dir must be outside the repository root")


def _validate_paper_spec(repo_root: Path, path: Path, account_id: str) -> None:
    document = _strict_json(path)
    actual_fields = set(document)
    missing_fields = sorted(_PAPER_ACCOUNT_REQUIRED_FIELDS - actual_fields)
    unknown_fields = sorted(actual_fields - _PAPER_ACCOUNT_REQUIRED_FIELDS - _PAPER_ACCOUNT_OPTIONAL_FIELDS)
    if missing_fields:
        raise DailyCycleError(
            f"paper spec {path} is missing required fields: {', '.join(missing_fields)}"
        )
    if unknown_fields:
        raise DailyCycleError(
            f"paper spec {path} contains unknown fields: {', '.join(unknown_fields)}"
        )
    if document["schema_version"] != _PAPER_SCHEMA_VERSION:
        raise DailyCycleError(f"paper spec {path} has unsupported schema_version")
    if document.get("account_id") != account_id:
        raise DailyCycleError(f"paper spec {path} account_id does not match the cycle entry")


def validate_cycle_spec(value: Mapping[str, Any], *, repo_root: str | Path) -> dict[str, Any]:
    """Validate a cycle specification and all operator-supplied file references."""

    root = Path(repo_root).resolve()
    value = _exact(
        value,
        {
            "$schema",
            "schema_version",
            "cycle_id",
            "session_date",
            "source",
            "run_key",
            "data_root",
            "ledger_root",
            "backup_dir",
            "report_path",
            "log_dir",
            "collection",
            "feature",
            "paper",
        },
        field="cycle",
    )
    if value["$schema"] != "../schemas/daily-cycle.schema.json":
        raise DailyCycleError("cycle.$schema is unsupported")
    if value["schema_version"] != SCHEMA_VERSION:
        raise DailyCycleError("cycle.schema_version is unsupported")
    cycle_id = _string(value["cycle_id"], field="cycle_id")
    if not _CYCLE_ID.fullmatch(cycle_id):
        raise DailyCycleError("cycle_id is not a safe cycle identifier")
    session_date = _string(value["session_date"], field="session_date")
    try:
        date.fromisoformat(session_date)
    except ValueError as error:
        raise DailyCycleError("session_date must be an ISO date") from error
    source = _string(value["source"], field="source")
    if not _SOURCE.fullmatch(source):
        raise DailyCycleError("source contains unsupported characters")
    run_key = _string(value["run_key"], field="run_key")
    if not _RUN_KEY.fullmatch(run_key):
        raise DailyCycleError("run_key contains unsupported characters")
    if value["data_root"] != "data":
        raise DailyCycleError("data_root must be the Compose-mounted data directory 'data'")
    ledger_root = _relative_path(value["ledger_root"], field="ledger_root")
    _safe_repo_path(root / "data", ledger_root, field="ledger_root")
    backup_dir = _validate_backup_path(value["backup_dir"], root)
    report_path = _relative_path(value["report_path"], field="report_path")
    log_dir = _relative_path(value["log_dir"], field="log_dir")

    collection = _exact(value["collection"], {"enabled"}, field="collection")
    if collection["enabled"] is not True:
        raise DailyCycleError("collection.enabled must be true for a complete cycle")

    feature = _exact(
        value["feature"],
        {
            "enabled",
            "universe",
            "schedule",
            "calendar_pin",
            "registry",
            "taxonomy_registry",
            "security_mappings",
            "feature_set",
            "feature_set_version",
        },
        field="feature",
    )
    if feature["enabled"] is not True:
        raise DailyCycleError("feature.enabled must be true for a complete cycle")
    feature_set = _string(feature["feature_set"], field="feature.feature_set")
    if not re.fullmatch(r"^[a-z0-9][a-z0-9-]{0,63}$", feature_set):
        raise DailyCycleError("feature.feature_set is not a safe feature-set name")
    feature_version = _string(feature["feature_set_version"], field="feature.feature_set_version")
    if not _SEMVER.fullmatch(feature_version):
        raise DailyCycleError("feature.feature_set_version must be semantic version x.y.z")
    for field in ("universe", "schedule", "calendar_pin", "registry", "taxonomy_registry"):
        _repo_file(root, feature[field], field=f"feature.{field}")
    security_mappings = feature["security_mappings"]
    if security_mappings is not None:
        _repo_file(root, security_mappings, field="feature.security_mappings")

    paper_entries = value["paper"]
    if not isinstance(paper_entries, list) or not paper_entries:
        raise DailyCycleError("paper must contain at least one account")
    normalized_paper: list[dict[str, str]] = []
    seen_accounts: set[str] = set()
    for index, raw in enumerate(paper_entries):
        entry = _exact(raw, {"account_id", "spec"}, field=f"paper[{index}]")
        account_id = _string(entry["account_id"], field=f"paper[{index}].account_id")
        try:
            parsed_id = UUID(account_id)
        except ValueError as error:
            raise DailyCycleError(f"paper[{index}].account_id must be a UUID") from error
        if str(parsed_id) != account_id or not _UUID.fullmatch(account_id):
            raise DailyCycleError(f"paper[{index}].account_id must be a lower-case canonical UUID")
        if account_id in seen_accounts:
            raise DailyCycleError(f"paper contains duplicate account {account_id}")
        seen_accounts.add(account_id)
        spec_path = _repo_file(root, entry["spec"], field=f"paper[{index}].spec")
        _validate_paper_spec(root, spec_path, account_id)
        normalized_paper.append({"account_id": account_id, "spec": _relative_path(entry["spec"], field=f"paper[{index}].spec")})

    return {
        "schema_version": SCHEMA_VERSION,
        "$schema": value["$schema"],
        "cycle_id": cycle_id,
        "session_date": session_date,
        "source": source,
        "run_key": run_key,
        "data_root": "data",
        "ledger_root": ledger_root,
        "backup_dir": backup_dir,
        "report_path": report_path,
        "log_dir": log_dir,
        "collection": {"enabled": True},
        "feature": {
            "enabled": True,
            "universe": _relative_path(feature["universe"], field="feature.universe"),
            "schedule": _relative_path(feature["schedule"], field="feature.schedule"),
            "calendar_pin": _relative_path(feature["calendar_pin"], field="feature.calendar_pin"),
            "registry": _relative_path(feature["registry"], field="feature.registry"),
            "taxonomy_registry": _relative_path(feature["taxonomy_registry"], field="feature.taxonomy_registry"),
            "security_mappings": (
                _relative_path(security_mappings, field="feature.security_mappings")
                if security_mappings is not None
                else None
            ),
            "feature_set": feature_set,
            "feature_set_version": feature_version,
        },
        "paper": normalized_paper,
    }


def load_cycle_spec(path: str | Path, *, repo_root: str | Path) -> dict[str, Any]:
    return validate_cycle_spec(_strict_json(Path(path)), repo_root=repo_root)


def build_plan(spec: Mapping[str, Any]) -> tuple[Stage, ...]:
    """Build the fixed dependency-ordered command plan from a validated spec."""

    feature = spec["feature"]
    stages: list[Stage] = [
        Stage("preflight", ("make", "release-validate")),
        Stage(
            "collection",
            ("make", "ingest", f"SOURCE={spec['source']}", f"RUN_KEY={spec['run_key']}"),
            ("preflight",),
        ),
        Stage("reconcile-before-derived", ("make", "reconcile"), ("collection",)),
        Stage(
            "feature-batch",
            tuple(
                [
                    "make",
                    "feature-batch",
                    f"BATCH_UNIVERSE={feature['universe']}",
                    f"BATCH_SCHEDULE={feature['schedule']}",
                    f"CALENDAR_PIN={feature['calendar_pin']}",
                    f"FEATURE_REGISTRY={feature['registry']}",
                    f"TAXONOMY_REGISTRY={feature['taxonomy_registry']}",
                    f"FEATURE_SET={feature['feature_set']}",
                    f"FEATURE_SET_VERSION={feature['feature_set_version']}",
                ]
                + (
                    [f"SECURITY_MAPPINGS={feature['security_mappings']}" ]
                    if feature["security_mappings"] is not None
                    else []
                )
            ),
            ("reconcile-before-derived",),
        ),
    ]
    paper_stage_names: list[str] = []
    for entry in spec["paper"]:
        account_id = entry["account_id"]
        prefix = f"paper:{account_id}"
        create_name = f"{prefix}:create"
        run_name = f"{prefix}:run"
        reconcile_name = f"{prefix}:reconcile"
        ledger = f"/data/{spec['ledger_root']}"
        stages.extend(
            [
                Stage(
                    create_name,
                    ("make", "paper-create-account", f"PAPER_SPEC={entry['spec']}", f"PAPER_LEDGER_ROOT={ledger}"),
                    ("feature-batch",),
                ),
                Stage(
                    run_name,
                    (
                        "make",
                        "paper-run",
                        f"PAPER_SPEC={entry['spec']}",
                        f"PAPER_LEDGER_ROOT={ledger}",
                        f"PAPER_SESSION_DATE={spec['session_date']}",
                    ),
                    (create_name,),
                ),
                Stage(
                    reconcile_name,
                    (
                        "make",
                        "paper-reconcile",
                        f"PAPER_ACCOUNT_ID={account_id}",
                        f"PAPER_LEDGER_ROOT={ledger}",
                    ),
                    (run_name,),
                ),
            ]
        )
        paper_stage_names.append(reconcile_name)
    stages.append(
        Stage(
            "backup",
            ("make", "backup-or-validate", f"BACKUP_DIR={spec['backup_dir']}"),
            ("preflight",),
        )
    )
    stages.extend(
        [
            Stage("reconcile-after-derived", ("make", "reconcile"), ("reconcile-before-derived",)),
            Stage("observe", ("make", "ops-status")),
        ]
    )
    return tuple(stages)


def _spec_hash(spec: Mapping[str, Any], *, root: Path) -> str:
    """Hash the cycle envelope and the bytes of every referenced input file."""

    material = dict(spec)
    feature = dict(spec["feature"])
    for field in ("universe", "schedule", "calendar_pin", "registry", "taxonomy_registry"):
        path = _repo_file(root, feature[field], field=f"feature.{field}")
        feature[f"{field}_sha256"] = _sha256_file(path)
    if feature["security_mappings"] is not None:
        path = _repo_file(root, feature["security_mappings"], field="feature.security_mappings")
        feature["security_mappings_sha256"] = _sha256_file(path)
    material["feature"] = feature
    material["paper"] = [
        {
            **entry,
            "spec_sha256": _sha256_file(
                _repo_file(root, entry["spec"], field=f"paper[{index}].spec")
            ),
        }
        for index, entry in enumerate(spec["paper"])
    ]
    encoded = json.dumps(material, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()
    return _sha256_bytes(encoded)


def _report_path(root: Path, spec: Mapping[str, Any]) -> Path:
    relative = _relative_path(spec["report_path"], field="report_path")
    return _safe_repo_path(root, relative, field="report_path")


def _load_existing_report(
    path: Path,
    *,
    root: Path,
    spec: Mapping[str, Any],
    stages: Sequence[Stage],
) -> dict[str, Any] | None:
    if not path.is_file():
        return None
    report = _strict_json(path)
    required = {"schema_version", "cycle_id", "session_date", "spec_sha256", "status", "started_at", "updated_at", "stages"}
    if set(report) != required:
        raise DailyCycleError(f"existing cycle report has an invalid field set: {path}")
    if report["schema_version"] != SCHEMA_VERSION or report["cycle_id"] != spec["cycle_id"] or report["session_date"] != spec["session_date"]:
        raise DailyCycleError("existing cycle report identity does not match the cycle spec")
    if report["spec_sha256"] != _spec_hash(spec, root=root):
        raise DailyCycleError("existing cycle report belongs to a different cycle specification")
    if not isinstance(report["stages"], list):
        raise DailyCycleError("existing cycle report stages must be an array")
    if report["status"] not in {"running", "passed", "attention"}:
        raise DailyCycleError("existing cycle report has an unsupported status")
    if len(report["stages"]) != len(stages):
        raise DailyCycleError("existing cycle report stage plan does not match the current cycle")
    for index, (item, stage) in enumerate(zip(report["stages"], stages, strict=True), start=1):
        if not isinstance(item, Mapping):
            raise DailyCycleError(f"existing cycle report stage {index} must be an object")
        expected_log = str(_stage_log_path(root, spec, index, stage.name).relative_to(root))
        if set(item) != {
            "name",
            "command",
            "status",
            "exit_code",
            "log_path",
            "started_at",
            "finished_at",
        }:
            raise DailyCycleError(f"existing cycle report stage {index} has an invalid field set")
        if item["name"] != stage.name or item["command"] != list(stage.command) or item["log_path"] != expected_log:
            raise DailyCycleError(f"existing cycle report stage {index} does not match the current plan")
        if item["status"] not in {"pending", "running", "passed", "failed", "skipped", "resumed"}:
            raise DailyCycleError(f"existing cycle report stage {index} has an unsupported status")
        if item["exit_code"] is not None and (
            not isinstance(item["exit_code"], int) or isinstance(item["exit_code"], bool)
        ):
            raise DailyCycleError(f"existing cycle report stage {index} has an invalid exit code")
        if item["status"] in {"passed", "resumed"} and item["exit_code"] != 0:
            raise DailyCycleError(f"existing cycle report stage {index} is marked passed without exit code 0")
        if item["status"] == "failed" and item["exit_code"] in {None, 0}:
            raise DailyCycleError(f"existing cycle report stage {index} is marked failed without a failure exit code")
        if item["status"] in {"pending", "running", "skipped"} and item["exit_code"] is not None:
            raise DailyCycleError(f"existing cycle report stage {index} has an exit code before completion")
        for timestamp_field in ("started_at", "finished_at"):
            timestamp = item[timestamp_field]
            if timestamp is not None:
                if not isinstance(timestamp, str) or not timestamp.endswith("Z"):
                    raise DailyCycleError(f"existing cycle report stage {index} has an invalid timestamp")
                try:
                    datetime.fromisoformat(timestamp)
                except ValueError as error:
                    raise DailyCycleError(
                        f"existing cycle report stage {index} has an invalid timestamp"
                    ) from error
    return report


def _write_report(path: Path, report: Mapping[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.is_symlink():
        raise DailyCycleError(f"report path must not be a symlink: {path}")
    temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    temporary_created = False
    try:
        with temporary.open("x", encoding="utf-8") as stream:
            temporary_created = True
            stream.write(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True) + "\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    except OSError as error:
        raise DailyCycleError(f"cannot write cycle report {path}: {error}") from error
    finally:
        if temporary_created:
            try:
                temporary.unlink()
            except FileNotFoundError:
                pass


def _stage_log_path(root: Path, spec: Mapping[str, Any], index: int, name: str) -> Path:
    slug = re.sub(r"[^A-Za-z0-9_.-]+", "_", name).strip("_")
    return root / spec["log_dir"] / f"{index:02d}-{slug}.log"


def _run_command(command: Sequence[str], *, root: Path, log_path: Path, environment: Mapping[str, str]) -> int:
    relative = log_path.relative_to(root).as_posix()
    safe_log_path = _safe_repo_path(root, relative, field="stage log path")
    safe_log_path.parent.mkdir(parents=True, exist_ok=True)
    with safe_log_path.open("ab") as log:
        process = subprocess.run(command, cwd=root, env=dict(environment), stdout=log, stderr=subprocess.STDOUT, check=False)
    return process.returncode


def run_cycle(spec: Mapping[str, Any], *, repo_root: str | Path) -> dict[str, Any]:
    """Execute or resume a validated cycle, writing the report after every stage."""

    root = Path(repo_root).resolve()
    normalized = validate_cycle_spec(spec, repo_root=root)
    stages = build_plan(normalized)
    report_path = _report_path(root, normalized)
    lock_path = root / ".runtime" / "daily.lock"
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    with lock_path.open("w") as lock:
        try:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise DailyCycleError(f"daily cycle already active: {lock_path}") from error

        existing = _load_existing_report(report_path, root=root, spec=normalized, stages=stages)
        now = _timestamp()
        if existing is None:
            report: dict[str, Any] = {
                "schema_version": SCHEMA_VERSION,
                "cycle_id": normalized["cycle_id"],
                "session_date": normalized["session_date"],
                "spec_sha256": _spec_hash(normalized, root=root),
                "status": "running",
                "started_at": now,
                "updated_at": now,
                "stages": [
                    {
                        "name": stage.name,
                        "command": list(stage.command),
                        "status": "pending",
                        "exit_code": None,
                        "log_path": str(_stage_log_path(root, normalized, index, stage.name).relative_to(root)),
                        "started_at": None,
                        "finished_at": None,
                    }
                    for index, stage in enumerate(stages, start=1)
                ],
            }
            _write_report(report_path, report)
        else:
            report = existing

        environment = os.environ.copy()
        environment["INVS_DATA_DIR"] = str(root / "data")
        environment["INVS_BACKUP_ROOT"] = normalized["backup_dir"]
        status_by_name = {item["name"]: item["status"] for item in report["stages"]}
        for index, stage in enumerate(stages, start=1):
            entry = report["stages"][index - 1]
            if entry["status"] in {"passed", "resumed"}:
                entry["status"] = "resumed"
                status_by_name[stage.name] = "resumed"
                continue
            if any(status_by_name.get(dependency) not in {"passed", "resumed"} for dependency in stage.dependencies):
                entry["status"] = "skipped"
                entry["exit_code"] = None
                entry["started_at"] = None
                entry["finished_at"] = _timestamp()
                status_by_name[stage.name] = "skipped"
                report["updated_at"] = entry["finished_at"]
                _write_report(report_path, report)
                print(f"stage={stage.name} status=skipped")
                continue
            entry["status"] = "running"
            entry["started_at"] = _timestamp()
            report["updated_at"] = entry["started_at"]
            _write_report(report_path, report)
            try:
                exit_code = _run_command(
                    stage.command,
                    root=root,
                    log_path=root / entry["log_path"],
                    environment=environment,
                )
            except OSError as error:
                exit_code = 127
                with (root / entry["log_path"]).open("ab") as log:
                    log.write(f"daily cycle could not execute stage: {error}\n".encode())
            entry["exit_code"] = exit_code
            entry["finished_at"] = _timestamp()
            entry["status"] = "passed" if exit_code == 0 else "failed"
            status_by_name[stage.name] = entry["status"]
            report["updated_at"] = entry["finished_at"]
            _write_report(report_path, report)
            print(f"stage={stage.name} status={entry['status']} exit_code={exit_code}")

        required_statuses = {"passed", "resumed"}
        report["status"] = "passed" if all(item["status"] in required_statuses for item in report["stages"]) else "attention"
        report["updated_at"] = _timestamp()
        _write_report(report_path, report)
        print(f"cycle_status={report['status']} report={report_path}")
        return report


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Run one explicit resumable local research cycle")
    parser.add_argument("--spec", required=True, help="cycle specification JSON")
    parser.add_argument("--repo-root", default=".")
    parser.add_argument("--dry-run", action="store_true", help="print the fixed command plan without executing it")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        spec = load_cycle_spec(args.spec, repo_root=args.repo_root)
        plan = build_plan(spec)
        if args.dry_run:
            print(
                json.dumps(
                    {
                        "cycle_id": spec["cycle_id"],
                        "session_date": spec["session_date"],
                        "status": "dry-run",
                        "stages": [
                            {"name": stage.name, "dependencies": list(stage.dependencies), "command": list(stage.command)}
                            for stage in plan
                        ],
                    },
                    indent=2,
                    sort_keys=True,
                )
            )
            return 0
        report = run_cycle(spec, repo_root=args.repo_root)
        return 0 if report["status"] == "passed" else 1
    except DailyCycleError as error:
        print(f"daily-cycle: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
