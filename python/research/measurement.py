"""Deterministic, policy-pinned prediction measurements for v0.4 fixtures."""

from __future__ import annotations

from datetime import datetime
from decimal import Decimal, InvalidOperation, localcontext
from pathlib import Path
from typing import Any, Final
from uuid import UUID, uuid5

from .documents import (
    DocumentArtifactValidationError,
    _hash,
    _nonempty,
    _sha256_bytes,
    _strict_json,
    _timestamp,
)

MEASUREMENT_POLICY_VERSION: Final[str] = "close_to_close_equal_weight_v1"
_OUTCOME_NAMESPACE = UUID("e13de5b4-39c9-5a48-96dc-c367f93bc1ba")
_PRICE_ARTIFACT_FIELDS = frozenset({"schema_version", "artifact_id", "available_at", "rows"})
_PRICE_ROW_FIELDS = frozenset({"asset_id", "observed_at", "close"})


class MeasurementError(ValueError):
    """Raised when a prediction cannot be measured from pinned data."""


class MeasurementValidationError(MeasurementError):
    """Raised when a pinned measurement artifact is malformed."""


def _measurement_nonempty(value: Any, *, field: str) -> str:
    try:
        return _nonempty(value, field=field)
    except DocumentArtifactValidationError as error:
        raise MeasurementValidationError(str(error)) from error


def _measurement_timestamp(value: datetime | str, *, field: str) -> tuple[str, datetime]:
    try:
        return _timestamp(value, field=field)
    except DocumentArtifactValidationError as error:
        raise MeasurementValidationError(str(error)) from error


def _measurement_hash(value: Any, *, field: str) -> str:
    try:
        result = _hash(value, field=field)
    except DocumentArtifactValidationError as error:
        raise MeasurementValidationError(str(error)) from error
    assert result is not None
    return result


def _decimal(value: Any, *, field: str) -> Decimal:
    try:
        parsed = Decimal(str(value))
    except (InvalidOperation, ValueError) as error:
        raise MeasurementValidationError(f"{field} must be a finite decimal") from error
    if not parsed.is_finite() or parsed <= 0:
        raise MeasurementValidationError(f"{field} must be a positive finite decimal")
    return parsed


def _read_price_artifact(path: str | Path, *, expected_sha256: str | None = None) -> dict[str, Any]:
    artifact_path = Path(path).expanduser().resolve()
    if expected_sha256 is not None and _measurement_hash(expected_sha256, field="artifact sha256") != _sha256_file(artifact_path):
        raise MeasurementValidationError(f"pinned price artifact hash mismatch: {artifact_path}")
    document = _strict_json(artifact_path, label="price measurement artifact")
    if frozenset(document) != _PRICE_ARTIFACT_FIELDS:
        raise MeasurementValidationError("price measurement artifact fields are invalid")
    if document["schema_version"] != "1.0.0":
        raise MeasurementValidationError("unsupported price measurement artifact schema_version")
    _measurement_nonempty(document["artifact_id"], field="artifact_id")
    _, available_at = _measurement_timestamp(document["available_at"], field="available_at")
    rows = document["rows"]
    if not isinstance(rows, list) or not rows:
        raise MeasurementValidationError("price measurement artifact rows must be non-empty")
    validated_rows: list[dict[str, Any]] = []
    for index, row in enumerate(rows):
        if not isinstance(row, dict) or frozenset(row) != _PRICE_ROW_FIELDS:
            raise MeasurementValidationError(f"price rows[{index}] fields are invalid")
        asset_id = _measurement_nonempty(row["asset_id"], field=f"rows[{index}].asset_id")
        observed_text, observed_at = _measurement_timestamp(row["observed_at"], field=f"rows[{index}].observed_at")
        close = _decimal(row["close"], field=f"rows[{index}].close")
        validated_rows.append({"asset_id": asset_id, "observed_at": observed_text, "observed": observed_at, "close": close})
    validated_rows.sort(key=lambda row: (row["asset_id"], row["observed"], row["close"]))
    return {"artifact": document, "available": available_at, "rows": validated_rows, "sha256": _sha256_file(artifact_path), "path": artifact_path}


def _sha256_file(path: Path) -> str:
    try:
        return _sha256_bytes(path.read_bytes())
    except OSError as error:
        raise MeasurementValidationError(f"cannot read price measurement artifact {path}: {error}") from error


def _return(start: Decimal, end: Decimal) -> Decimal:
    with localcontext() as context:
        context.prec = 50
        return (end / start) - Decimal(1)


def _max_drawdown(series: list[Decimal]) -> Decimal:
    if not series:
        return Decimal(0)
    peak = series[0]
    worst = Decimal(0)
    with localcontext() as context:
        context.prec = 50
        for value in series:
            peak = max(peak, value)
            drawdown = (value / peak) - Decimal(1)
            worst = min(worst, drawdown)
    return worst


def _decimal_text(value: Decimal) -> str:
    return format(value, "f")


def measure_prediction(
    prediction: dict[str, Any],
    *,
    price_artifact: str | Path,
    price_artifact_sha256: str,
    benchmark_asset_id: str,
    measured_at: datetime | str,
    input_artifact_refs: list[dict[str, Any]],
) -> dict[str, Any]:
    """Measure a frozen prediction from one hash-pinned price artifact.

    The fixture format supplies two or more close observations per asset. The
    policy computes an equal-weight basket return from the first and last
    observation and compares it with the same-window benchmark return.
    """

    if not isinstance(prediction, dict) or prediction.get("status") != "frozen":
        raise MeasurementValidationError("only frozen predictions can be measured")
    direction = _measurement_nonempty(prediction.get("expected_direction"), field="expected_direction")
    _, measurement_time = _measurement_timestamp(measured_at, field="measured_at")
    artifact = _read_price_artifact(price_artifact, expected_sha256=price_artifact_sha256)
    if artifact["available"] > measurement_time:
        raise MeasurementValidationError("price artifact is not available at measured_at")
    expected_assets = prediction.get("asset_or_universe")
    if not isinstance(expected_assets, list) or not expected_assets:
        raise MeasurementValidationError("prediction asset_or_universe must be non-empty")
    asset_ids = {_measurement_nonempty(value, field="asset_or_universe") for value in expected_assets}
    benchmark_asset_id = _measurement_nonempty(benchmark_asset_id, field="benchmark_asset_id")
    if benchmark_asset_id in asset_ids:
        raise MeasurementValidationError("benchmark must not be part of the predicted basket")
    rows_by_asset: dict[str, list[dict[str, Any]]] = {}
    for row in artifact["rows"]:
        if row["observed"] <= measurement_time:
            rows_by_asset.setdefault(row["asset_id"], []).append(row)
    for asset_id in [*asset_ids, benchmark_asset_id]:
        if asset_id not in rows_by_asset or len(rows_by_asset[asset_id]) < 2:
            raise MeasurementValidationError(f"measurement data is incomplete for {asset_id}")
    basket_returns: list[Decimal] = []
    basket_series: list[Decimal] = []
    for asset_id in sorted(asset_ids):
        series = sorted(rows_by_asset[asset_id], key=lambda row: row["observed"])
        basket_returns.append(_return(series[0]["close"], series[-1]["close"]))
        basket_series.extend(row["close"] for row in series)
    benchmark_series = sorted(rows_by_asset[benchmark_asset_id], key=lambda row: row["observed"])
    with localcontext() as context:
        context.prec = 50
        basket_return = sum(basket_returns, Decimal(0)) / Decimal(len(basket_returns))
    benchmark_return = _return(benchmark_series[0]["close"], benchmark_series[-1]["close"])
    relative_return = basket_return - benchmark_return
    if direction == "up":
        hit = basket_return > 0
    elif direction == "down":
        hit = basket_return < 0
    elif direction == "flat":
        hit = basket_return == 0
    elif direction == "relative_outperformance":
        hit = relative_return > 0
    elif direction == "relative_underperformance":
        hit = relative_return < 0
    else:
        raise MeasurementValidationError(f"unsupported prediction direction {direction!r}")
    refs = []
    for index, ref in enumerate(input_artifact_refs):
        if not isinstance(ref, dict) or not {"kind", "id", "sha256"} <= frozenset(ref):
            raise MeasurementValidationError(f"input_artifact_refs[{index}] requires kind, id, sha256")
        refs.append(
            {
                "kind": _measurement_nonempty(ref["kind"], field=f"input_artifact_refs[{index}].kind"),
                "id": _measurement_nonempty(ref["id"], field=f"input_artifact_refs[{index}].id"),
                "sha256": _measurement_hash(ref["sha256"], field=f"input_artifact_refs[{index}].sha256"),
            }
        )
    if not refs:
        raise MeasurementValidationError("at least one input artifact reference is required")
    return {
        "schema_version": "1.0.0",
        "outcome_id": str(uuid5(_OUTCOME_NAMESPACE, f"{prediction['id']}:{MEASUREMENT_POLICY_VERSION}")),
        "prediction_id": prediction["id"],
        "measurement_policy_version": MEASUREMENT_POLICY_VERSION,
        "status": "measured",
        "realized_result": {
            "basket_return": _decimal_text(basket_return),
            "assets": sorted(asset_ids),
            "hit": hit,
        },
        "benchmark_result": {
            "asset_id": benchmark_asset_id,
            "return": _decimal_text(benchmark_return),
            "relative_return": _decimal_text(relative_return),
        },
        "drawdown": float(_max_drawdown(basket_series)),
        "measured_at": _measurement_timestamp(measured_at, field="measured_at")[0],
        "input_artifact_refs": refs,
    }


__all__ = ["MEASUREMENT_POLICY_VERSION", "MeasurementError", "MeasurementValidationError", "measure_prediction"]
