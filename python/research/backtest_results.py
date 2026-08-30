"""Immutable publication, validation, and comparison of backtest results."""

from __future__ import annotations

import hashlib
import json
import os
import re
import tempfile
from collections.abc import Iterable
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from uuid import UUID

from .backtest import BACKTEST_SCHEMA_VERSION, BacktestRun

_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_UUID = re.compile(
    r"^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"
)
_DECIMAL = re.compile(r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$")
_RESULT_FIELDS = frozenset(
    {
        "schema_version",
        "result_id",
        "experiment_id",
        "experiment_sha256",
        "engine_version",
        "input_fingerprint",
        "status",
        "context",
        "artifacts",
        "summary",
        "metrics",
    }
)
_ARTIFACT_FIELDS = frozenset({"path", "sha256", "row_count"})
_OUTPUT_ROW_FIELDS = {
    "nav": frozenset(
        {
            "session_date",
            "partition",
            "decision_at",
            "cash_by_currency",
            "cash_base",
            "positions_value_base",
            "nav",
            "gross_exposure",
            "net_exposure",
            "benchmark_nav",
            "reporting_nav",
        }
    ),
    "holdings": frozenset(
        {
            "session_date",
            "security_id",
            "quantity",
            "price",
            "currency",
            "market_value_local",
            "market_value_base",
            "position_weight",
        }
    ),
    "orders": frozenset(
        {
            "order_id",
            "decision_session",
            "execution_session",
            "security_id",
            "side",
            "quantity",
            "reference_price",
            "currency",
            "target_weight",
            "reason",
            "status",
        }
    ),
    "fills": frozenset(
        {
            "fill_id",
            "order_id",
            "execution_at",
            "execution_session",
            "security_id",
            "side",
            "quantity",
            "reference_price",
            "fill_price",
            "gross_notional",
            "fee",
            "spread_cost",
            "slippage_cost",
            "tax",
            "currency",
            "base_notional",
            "status",
        }
    ),
    "ledger": frozenset(
        {
            "sequence",
            "session_date",
            "event_at",
            "event_type",
            "security_id",
            "currency",
            "quantity",
            "amount_local",
            "amount_base",
            "price",
            "note",
        }
    ),
}
_OUTPUT_REQUIRED_FIELDS = {
    "nav": frozenset(
        {
            "session_date",
            "partition",
            "decision_at",
            "cash_by_currency",
            "cash_base",
            "positions_value_base",
            "nav",
            "gross_exposure",
            "net_exposure",
            "benchmark_nav",
        }
    ),
    "holdings": _OUTPUT_ROW_FIELDS["holdings"],
    "orders": _OUTPUT_ROW_FIELDS["orders"],
    "fills": _OUTPUT_ROW_FIELDS["fills"],
    "ledger": frozenset(
        {"sequence", "session_date", "event_at", "event_type", "currency", "amount_local", "amount_base", "note"}
    ),
}


class BacktestResultError(ValueError):
    """Base error for result publication and validation."""


class BacktestResultConflictError(BacktestResultError):
    """Raised when an immutable result path contains different bytes."""


class BacktestResultValidationError(BacktestResultError):
    """Raised when a result manifest or output artifact is invalid."""


@dataclass(frozen=True)
class ValidatedBacktestResult:
    manifest_path: Path
    manifest: dict[str, Any]
    artifacts: dict[str, Any]


def _sha256_bytes(content: bytes) -> str:
    return hashlib.sha256(content).hexdigest()


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise BacktestResultValidationError(f"cannot hash result artifact {path}: {error}") from error
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
            parse_constant=lambda value: (_ for _ in ()).throw(
                ValueError(f"invalid JSON constant {value}")
            ),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise BacktestResultValidationError(f"invalid result JSON {path}: {error}") from error


def _uuid(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _UUID.fullmatch(value):
        raise BacktestResultValidationError(f"{field} must be a canonical UUID")
    if str(UUID(value)) != value:
        raise BacktestResultValidationError(f"{field} must be a canonical UUID")
    return value


def _sha(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not _SHA256.fullmatch(value):
        raise BacktestResultValidationError(f"{field} must be a lower-case SHA-256")
    return value


def _safe_relative(path: Any, *, field: str) -> str:
    if not isinstance(path, str) or not path or path.startswith(("/", "\\")) or "\x00" in path:
        raise BacktestResultValidationError(f"{field} must be a safe relative path")
    parts = path.replace("\\", "/").split("/")
    if any(part in {"", ".", ".."} for part in parts):
        raise BacktestResultValidationError(f"{field} must be a safe relative path")
    return path


def _write_immutable(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        if not path.is_file() or _sha256_file(path) != _sha256_bytes(content):
            raise BacktestResultConflictError(f"immutable result path conflicts: {path}")
        return
    temporary: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(dir=path.parent, prefix=f".{path.name}.", delete=False) as stream:
            temporary = Path(stream.name)
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        try:
            os.link(temporary, path)
        except FileExistsError:
            if not path.is_file() or _sha256_file(path) != _sha256_bytes(content):
                raise BacktestResultConflictError(f"result path appeared with different bytes: {path}")
        finally:
            temporary.unlink(missing_ok=True)
    except OSError as error:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
        raise BacktestResultError(f"cannot publish result {path}: {error}") from error


def _artifact_documents(run: BacktestRun) -> dict[str, Any]:
    return {
        "nav": list(run.nav),
        "holdings": list(run.holdings),
        "orders": list(run.orders),
        "fills": list(run.fills),
        "ledger": list(run.ledger),
        "metrics": run.metrics,
    }


def publish_backtest_result(run: BacktestRun, *, results_root: str | Path) -> Path:
    """Publish one completed run as an immutable manifest and six JSON artifacts."""

    documents = _artifact_documents(run)
    root = Path(results_root).expanduser().resolve()
    directory = root / f"experiment-{run.experiment_id}" / f"result-{run.result_id}"
    manifest_path = directory / "manifest.json"
    artifact_metadata: dict[str, dict[str, Any]] = {}
    contents: dict[str, bytes] = {}
    for kind, document in documents.items():
        content = (
            json.dumps(document, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
            + "\n"
        ).encode("utf-8")
        contents[kind] = content
        artifact_metadata[kind] = {
            "path": f"{kind}.json",
            "sha256": _sha256_bytes(content),
            "row_count": 1 if kind == "metrics" else len(document),
        }
    manifest = {
        "schema_version": BACKTEST_SCHEMA_VERSION,
        "result_id": run.result_id,
        "experiment_id": run.experiment_id,
        "experiment_sha256": run.experiment_sha256,
        "engine_version": run.engine_version,
        "input_fingerprint": run.input_fingerprint,
        "status": "completed",
        "context": run.context,
        "artifacts": artifact_metadata,
        "summary": run.summary,
        "metrics": run.metrics,
    }
    manifest_content = (
        json.dumps(manifest, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False) + "\n"
    ).encode("utf-8")
    if directory.exists() and (not directory.is_dir() or not manifest_path.is_file()):
        raise BacktestResultConflictError(f"result directory is incomplete: {directory}")
    for kind, content in contents.items():
        _write_immutable(directory / f"{kind}.json", content)
    _write_immutable(manifest_path, manifest_content)
    return manifest_path


def _validate_output_row(kind: str, row: Any, index: int) -> None:
    if not isinstance(row, dict):
        raise BacktestResultValidationError(f"{kind} row {index} must be an object")
    allowed = _OUTPUT_ROW_FIELDS[kind]
    required = _OUTPUT_REQUIRED_FIELDS[kind]
    if not set(row).issubset(allowed) or not required.issubset(row):
        raise BacktestResultValidationError(f"{kind} row {index} has missing or unknown fields")
    decimal_fields = {
        "quantity",
        "reference_price",
        "fill_price",
        "gross_notional",
        "fee",
        "spread_cost",
        "slippage_cost",
        "tax",
        "base_notional",
        "amount_local",
        "amount_base",
        "price",
        "cash_base",
        "positions_value_base",
        "nav",
        "gross_exposure",
        "net_exposure",
        "benchmark_nav",
        "reporting_nav",
        "market_value_local",
        "market_value_base",
        "position_weight",
        "target_weight",
    }
    for field, value in row.items():
        if field in decimal_fields and value is not None and (
            not isinstance(value, str) or not _DECIMAL.fullmatch(value)
        ):
            raise BacktestResultValidationError(f"{kind} row {index}.{field} must be a decimal string")
    if kind in {"orders", "fills"}:
        for field in ("order_id", "security_id"):
            _uuid(row[field], field=f"{kind} row {index}.{field}")
    if kind == "fills":
        _uuid(row["fill_id"], field=f"fills row {index}.fill_id")
    if kind == "holdings":
        _uuid(row["security_id"], field=f"holdings row {index}.security_id")
    if kind == "ledger" and "security_id" in row:
        _uuid(row["security_id"], field=f"ledger row {index}.security_id")


def _validate_metrics(metrics: Any) -> None:
    required = {
        "schema_version",
        "metrics_version",
        "base_currency",
        "annualization_factor",
        "risk_free_annual",
        "values",
        "attribution",
    }
    if not isinstance(metrics, dict) or set(metrics) != required:
        raise BacktestResultValidationError("metrics artifact has missing or unknown fields")
    if metrics["schema_version"] != BACKTEST_SCHEMA_VERSION or metrics["metrics_version"] != "1.0.0":
        raise BacktestResultValidationError("metrics artifact version is unsupported")
    if not isinstance(metrics["values"], dict) or not metrics["values"]:
        raise BacktestResultValidationError("metrics values must be a non-empty object")
    for key, value in metrics["values"].items():
        if not isinstance(key, str) or (
            value is not None and (not isinstance(value, str) or not _DECIMAL.fullmatch(value))
        ):
            raise BacktestResultValidationError(f"metrics value {key!r} is invalid")
    attribution = metrics["attribution"]
    if not isinstance(attribution, dict) or set(attribution) != {"by_partition", "by_year"}:
        raise BacktestResultValidationError("metrics attribution is invalid")
    for group in attribution.values():
        if not isinstance(group, dict):
            raise BacktestResultValidationError("metrics attribution group is invalid")
        for row in group.values():
            if not isinstance(row, dict) or set(row) != {
                "start_nav",
                "end_nav",
                "return",
                "trade_count",
                "cost",
            }:
                raise BacktestResultValidationError("metrics attribution row is invalid")
            for field in ("start_nav", "end_nav", "return", "cost"):
                if not isinstance(row[field], str) or not _DECIMAL.fullmatch(row[field]):
                    raise BacktestResultValidationError("metrics attribution decimal is invalid")


def read_backtest_result(manifest_path: str | Path) -> ValidatedBacktestResult:
    """Validate a result manifest, all hashes, row counts, and output shapes."""

    path = Path(manifest_path).expanduser().resolve()
    if path.name != "manifest.json":
        raise BacktestResultValidationError("result manifest must be named manifest.json")
    manifest = _strict_json(path)
    if not isinstance(manifest, dict) or set(manifest) != _RESULT_FIELDS:
        raise BacktestResultValidationError("result manifest has missing or unknown fields")
    if manifest["schema_version"] != BACKTEST_SCHEMA_VERSION or manifest["status"] != "completed":
        raise BacktestResultValidationError("result manifest version or status is unsupported")
    experiment_id = _uuid(manifest["experiment_id"], field="experiment_id")
    result_id = _uuid(manifest["result_id"], field="result_id")
    _sha(manifest["experiment_sha256"], field="experiment_sha256")
    _sha(manifest["input_fingerprint"], field="input_fingerprint")
    if not isinstance(manifest["engine_version"], str) or not manifest["engine_version"]:
        raise BacktestResultValidationError("engine_version must be non-empty")
    if not isinstance(manifest["artifacts"], dict) or set(manifest["artifacts"]) != {
        "nav",
        "holdings",
        "orders",
        "fills",
        "ledger",
        "metrics",
    }:
        raise BacktestResultValidationError("result artifacts are incomplete")
    if not isinstance(manifest["summary"], dict):
        raise BacktestResultValidationError("result summary must be an object")
    result_directory = path.parent
    expected_files = {"manifest.json"}
    artifacts: dict[str, Any] = {}
    for kind, metadata in manifest["artifacts"].items():
        if not isinstance(metadata, dict) or set(metadata) != _ARTIFACT_FIELDS:
            raise BacktestResultValidationError(f"result artifact metadata for {kind} is invalid")
        relative = _safe_relative(metadata["path"], field=f"artifacts.{kind}.path")
        artifact_path = (result_directory / relative).resolve()
        try:
            artifact_path.relative_to(result_directory)
        except ValueError as error:
            raise BacktestResultValidationError(f"result artifact {kind} escapes its directory") from error
        expected_files.add(artifact_path.name)
        expected_sha256 = _sha(metadata["sha256"], field=f"artifacts.{kind}.sha256")
        if not artifact_path.is_file() or _sha256_file(artifact_path) != expected_sha256:
            raise BacktestResultValidationError(f"result artifact {kind} hash or file is invalid")
        document = _strict_json(artifact_path)
        if kind == "metrics":
            _validate_metrics(document)
            actual_count = 1
        else:
            if not isinstance(document, list):
                raise BacktestResultValidationError(f"result artifact {kind} must be an array")
            for index, row in enumerate(document):
                _validate_output_row(kind, row, index)
            actual_count = len(document)
        if metadata["row_count"] != actual_count:
            raise BacktestResultValidationError(f"result artifact {kind} row count is incorrect")
        artifacts[kind] = document
    unexpected = {item.name for item in result_directory.iterdir() if item.is_file()} - expected_files
    if unexpected:
        raise BacktestResultValidationError(
            f"result directory contains unlisted files: {', '.join(sorted(unexpected))}"
        )
    if artifacts["metrics"] != manifest["metrics"]:
        raise BacktestResultValidationError("top-level metrics disagree with metrics artifact")
    summary_counts = {
        "session_count": len(artifacts["nav"]),
        "holdings_count": len(artifacts["holdings"]),
        "order_count": len(artifacts["orders"]),
        "fill_count": len(artifacts["fills"]),
        "ledger_count": len(artifacts["ledger"]),
    }
    for key, count in summary_counts.items():
        if manifest["summary"].get(key) != count:
            raise BacktestResultValidationError(f"summary {key} disagrees with artifact")
    if not isinstance(manifest["context"], dict) or not manifest["context"]:
        raise BacktestResultValidationError("result context must be an object")
    if (
        path.parent.name != f"result-{result_id}"
        or path.parent.parent.name != f"experiment-{experiment_id}"
    ):
        raise BacktestResultValidationError("result manifest path does not match immutable identity")
    return ValidatedBacktestResult(path, manifest, artifacts)


def compare_backtest_results(manifest_paths: Iterable[str | Path]) -> dict[str, Any]:
    """Return a deterministic read-only comparison table for completed results."""

    results = [read_backtest_result(path) for path in manifest_paths]
    rows = []
    for result in results:
        manifest = result.manifest
        context = manifest["context"]
        values = manifest["metrics"]["values"]
        rows.append(
            {
                "result_id": manifest["result_id"],
                "experiment_id": manifest["experiment_id"],
                "strategy": context["strategy"],
                "period": context["period"],
                "universe": context["universe"],
                "cost_policy": context["cost_policy"],
                "base_currency": context["base_currency"],
                "reporting_currency": context["reporting_currency"],
                "total_return": values.get("total_return"),
                "cagr": values.get("cagr"),
                "max_drawdown": values.get("max_drawdown"),
                "total_cost": values.get("total_cost"),
                "rejected_trade_count": values.get("rejected_trade_count"),
            }
        )
    rows.sort(key=lambda row: (row["period"]["start_date"], row["strategy"]["name"], row["result_id"]))
    return {"schema_version": BACKTEST_SCHEMA_VERSION, "results": rows}


__all__ = [
    "BacktestResultConflictError",
    "BacktestResultError",
    "BacktestResultValidationError",
    "ValidatedBacktestResult",
    "compare_backtest_results",
    "publish_backtest_result",
    "read_backtest_result",
]
