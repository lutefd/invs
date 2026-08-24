"""Deterministic backward-adjusted price artifacts for corporate-action policy 1.0.0."""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from datetime import UTC, datetime
from decimal import ROUND_HALF_EVEN, Decimal, localcontext
from fractions import Fraction
from pathlib import Path
from typing import Any, Final
from uuid import UUID, uuid5

POLICY_VERSION: Final[str] = "backward_split_dividend_1_0_0"
SCHEMA_VERSION: Final[str] = "1.0.0"
MANIFEST_VERSION: Final[str] = "1.0.0"
GENERATOR_VERSION: Final[str] = "python-adjustments-1.0.0"
NON_TERMINATING_SIGNIFICANT_DIGITS: Final[int] = 50

_ARTIFACT_NAMESPACE = UUID("4a633f8c-58b6-5f0d-a33a-59a99555b1b7")
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_DECIMAL = re.compile(r"^(0|[1-9][0-9]*)(\.[0-9]+)?$")
_UTC_TIMESTAMP = re.compile(
    r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$"
)
_PART = re.compile(r"^part-([0-9a-f]{64})\.parquet$")
_NORMALIZED_MANIFEST_FIELDS = frozenset(
    {
        "manifest_version",
        "schema_version",
        "normalizer_version",
        "git_commit",
        "source",
        "data_source_id",
        "ingestion_run_id",
        "partition",
        "row_count",
        "parts",
    }
)
_NORMALIZED_PART_FIELDS = frozenset({"path", "sha256", "row_count"})
_CORPORATE_ACTION_FIELDS = frozenset(
    {
        "schema_version",
        "id",
        "security_id",
        "source_event_id",
        "revision",
        "action_status",
        "action_type",
        "observed_at",
        "observed_precision",
        "published_at",
        "published_precision",
        "available_at",
        "effective_at",
        "effective_precision",
        "record_date",
        "payment_date",
        "ratio_numerator",
        "ratio_denominator",
        "cash_amount",
        "currency",
        "target_security_id",
        "source_reference",
        "recorded_at",
        "provenance",
    }
)
_ADJUSTMENT_MANIFEST_FIELDS = frozenset(
    {
        "schema_version",
        "manifest_version",
        "artifact_id",
        "policy_version",
        "generator_version",
        "security_id",
        "source",
        "interval",
        "currency",
        "decision_at",
        "created_at",
        "raw_price_manifest_path",
        "raw_price_manifest_sha256",
        "raw_price_part_path",
        "raw_price_part_sha256",
        "raw_price_basis",
        "corporate_action_snapshot_sha256",
        "selected_actions",
        "row_count",
        "parts",
    }
)
_OUTPUT_FIELDS = frozenset(
    {
        "schema_version",
        "artifact_id",
        "policy_version",
        "security_id",
        "source",
        "interval",
        "currency",
        "decision_at",
        "observed_at",
        "available_at",
        "raw_open",
        "raw_high",
        "raw_low",
        "raw_close",
        "raw_volume",
        "price_factor",
        "volume_factor",
        "adjusted_open",
        "adjusted_high",
        "adjusted_low",
        "adjusted_close",
        "adjusted_volume",
        "has_volume",
    }
)
_ACTION_PIN_FIELDS = (
    "id",
    "source_event_id",
    "revision",
    "record_sha256",
    "observed_at",
    "available_at",
    "effective_at",
)


class AdjustmentArtifactError(ValueError):
    """Raised when adjustment inputs cannot safely produce an artifact."""


class AdjustmentArtifactConflictError(AdjustmentArtifactError):
    """Raised when an immutable artifact identity already contains other bytes."""


class AdjustmentArtifactValidationError(AdjustmentArtifactError):
    """Raised when a published adjustment artifact fails validation."""


@dataclass(frozen=True)
class ValidatedAdjustmentArtifact:
    manifest_path: Path
    manifest: dict[str, Any]
    rows: tuple[dict[str, Any], ...]
    part_path: Path


def _json_object_without_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key {key!r}")
        result[key] = value
    return result


def _read_json(path: Path, *, label: str) -> dict[str, Any]:
    try:
        result = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=_json_object_without_duplicates,
            parse_constant=lambda value: (_ for _ in ()).throw(
                ValueError(f"invalid JSON constant {value}")
            ),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise AdjustmentArtifactValidationError(f"invalid {label} {path}: {error}") from error
    if not isinstance(result, dict):
        raise AdjustmentArtifactValidationError(f"invalid {label} {path}: expected object")
    return result


def _canonical_json(value: Any) -> bytes:
    return json.dumps(
        value,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")


def _sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise AdjustmentArtifactValidationError(f"cannot hash {path}: {error}") from error
    return digest.hexdigest()


def _canonical_timestamp(value: Any, *, field: str) -> tuple[str, datetime]:
    candidate = value
    if hasattr(candidate, "to_pydatetime"):
        candidate = candidate.to_pydatetime(warn=False)
    if isinstance(candidate, datetime):
        parsed = candidate
    elif isinstance(candidate, str):
        text = candidate[:-1] + "+00:00" if candidate.endswith("Z") else candidate
        try:
            parsed = datetime.fromisoformat(text)
        except ValueError as error:
            raise AdjustmentArtifactError(f"{field} must be RFC 3339 UTC") from error
    else:
        raise AdjustmentArtifactError(f"{field} must be RFC 3339 UTC")
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise AdjustmentArtifactError(f"{field} must include a timezone")
    utc = parsed.astimezone(UTC)
    fraction = f".{utc.microsecond:06d}".rstrip("0") if utc.microsecond else ""
    canonical = utc.strftime("%Y-%m-%dT%H:%M:%S") + fraction + "Z"
    if not _UTC_TIMESTAMP.fullmatch(canonical):
        raise AdjustmentArtifactError(f"{field} is outside the timestamp contract")
    return canonical, utc


def _canonical_uuid(value: Any, *, field: str) -> str:
    try:
        canonical = str(UUID(value))
    except (AttributeError, TypeError, ValueError) as error:
        raise AdjustmentArtifactError(f"{field} must be a UUID") from error
    if value != canonical:
        raise AdjustmentArtifactError(f"{field} must be a canonical UUID")
    return canonical


def _fraction(value: Any, *, field: str, positive: bool = False) -> Fraction:
    if not isinstance(value, str) or not _DECIMAL.fullmatch(value):
        raise AdjustmentArtifactError(f"{field} must be a canonical non-negative decimal")
    result = Fraction(Decimal(value))
    if positive and result <= 0:
        raise AdjustmentArtifactError(f"{field} must be positive")
    return result


def _canonical_fraction(value: Fraction) -> str:
    if value < 0:
        raise AdjustmentArtifactError("adjusted decimal cannot be negative")
    denominator = value.denominator
    twos = 0
    fives = 0
    while denominator % 2 == 0:
        denominator //= 2
        twos += 1
    while denominator % 5 == 0:
        denominator //= 5
        fives += 1
    if denominator == 1:
        scale = max(twos, fives)
        scaled = value.numerator * 2 ** (scale - twos) * 5 ** (scale - fives)
        digits = str(scaled).rjust(scale + 1, "0")
        if scale == 0:
            return digits
        result = digits[:-scale] + "." + digits[-scale:]
        return result.rstrip("0").rstrip(".")
    with localcontext() as context:
        context.prec = NON_TERMINATING_SIGNIFICANT_DIGITS
        context.rounding = ROUND_HALF_EVEN
        result = format(Decimal(value.numerator) / Decimal(value.denominator), "f")
    return result.rstrip("0").rstrip(".")


def _validate_raw_manifest(path: Path) -> tuple[dict[str, Any], Path, str, str]:
    manifest = _read_json(path, label="raw price manifest")
    if set(manifest) != _NORMALIZED_MANIFEST_FIELDS:
        raise AdjustmentArtifactValidationError("raw price manifest has unsupported fields")
    partition = manifest.get("partition")
    if not isinstance(partition, dict) or partition.get("dataset") != "prices":
        raise AdjustmentArtifactValidationError("raw input manifest must partition prices")
    parts = manifest.get("parts")
    if not isinstance(parts, list) or len(parts) != 1:
        raise AdjustmentArtifactValidationError("policy 1.0.0 requires exactly one raw price part")
    part = parts[0]
    if not isinstance(part, dict) or set(part) != _NORMALIZED_PART_FIELDS:
        raise AdjustmentArtifactValidationError("raw price manifest part is invalid")
    name = part["path"]
    sha256 = part["sha256"]
    if (
        not isinstance(manifest.get("row_count"), int)
        or isinstance(manifest["row_count"], bool)
        or manifest["row_count"] < 0
        or not isinstance(part.get("row_count"), int)
        or isinstance(part["row_count"], bool)
        or part["row_count"] != manifest["row_count"]
    ):
        raise AdjustmentArtifactValidationError("raw price manifest row_count is invalid")
    match = _PART.fullmatch(name) if isinstance(name, str) else None
    if match is None or match.group(1) != sha256:
        raise AdjustmentArtifactValidationError("raw price part is not content-named")
    part_path = (path.parent / name).resolve()
    try:
        part_path.relative_to(path.parent.resolve())
    except ValueError as error:
        raise AdjustmentArtifactValidationError("raw price part escapes its manifest") from error
    if not part_path.is_file() or _sha256_file(part_path) != sha256:
        raise AdjustmentArtifactValidationError("raw price part is missing or has a hash mismatch")
    manifest_sha = _sha256_file(path)
    return manifest, part_path, manifest_sha, sha256


def _read_raw_rows(
    part_path: Path,
    *,
    security_id: str,
    decision_at: datetime,
    expected_row_count: int,
) -> tuple[list[dict[str, Any]], str, str, str]:
    from pyarrow import parquet

    table = parquet.ParquetFile(part_path).read()
    if table.num_rows != expected_row_count:
        raise AdjustmentArtifactValidationError("raw price manifest row_count mismatch")
    required = {
        "source",
        "security_id",
        "interval",
        "price_basis",
        "currency",
        "observed_at",
        "available_at",
        "open",
        "high",
        "low",
        "close",
        "volume",
        "has_volume",
    }
    if not required <= set(table.column_names):
        raise AdjustmentArtifactValidationError("raw price part lacks required canonical fields")
    rows: list[dict[str, Any]] = []
    identities: set[tuple[str, str, str]] = set()
    observed: set[str] = set()
    for index, row in enumerate(table.to_pylist()):
        if row["security_id"] != security_id:
            raise AdjustmentArtifactError(f"raw price row {index} has another security")
        if row["source"] == "yahoo":
            raise AdjustmentArtifactError(
                "Yahoo chart prices are split_adjusted and cannot be a raw adjustment input"
            )
        if row["price_basis"] != "raw" or row["interval"] != "1d":
            raise AdjustmentArtifactError("policy 1.0.0 requires raw daily prices")
        timestamp, moment = _canonical_timestamp(row["observed_at"], field="observed_at")
        _, available = _canonical_timestamp(row["available_at"], field="available_at")
        if moment > decision_at or available > decision_at:
            continue
        if timestamp in observed:
            raise AdjustmentArtifactError(f"duplicate raw price observation {timestamp}")
        observed.add(timestamp)
        identities.add((row["source"], row["interval"], row["currency"]))
        canonical = dict(row)
        canonical["observed_at"] = timestamp
        canonical["available_at"] = _canonical_timestamp(
            row["available_at"], field="available_at"
        )[0]
        rows.append(canonical)
    if not rows:
        raise AdjustmentArtifactError("no raw price rows are eligible at decision_at")
    if len(identities) != 1:
        raise AdjustmentArtifactError("raw price part mixes source, interval, or currency")
    rows.sort(key=lambda row: row["observed_at"])
    source, interval, currency = identities.pop()
    return rows, source, interval, currency


def _select_actions(
    actions: Iterable[Mapping[str, Any]],
    *,
    security_id: str,
    currency: str,
    decision_at: datetime,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    selected: list[dict[str, Any]] = []
    pins: list[dict[str, Any]] = []
    families: set[str] = set()
    for index, candidate in enumerate(actions):
        action = dict(candidate)
        if set(action) != _CORPORATE_ACTION_FIELDS or action.get("schema_version") != "2.0.0":
            raise AdjustmentArtifactError(f"corporate action {index} violates schema 2.0.0")
        if action.get("security_id") != security_id:
            raise AdjustmentArtifactError(f"corporate action {index} has another security")
        source_event_id = action.get("source_event_id")
        if not isinstance(source_event_id, str) or not source_event_id:
            raise AdjustmentArtifactError(f"corporate action {index} lacks source_event_id")
        if source_event_id in families:
            raise AdjustmentArtifactError(f"corporate action family {source_event_id!r} is unresolved")
        families.add(source_event_id)
        revision = action.get("revision")
        if not isinstance(revision, int) or isinstance(revision, bool) or revision < 0:
            raise AdjustmentArtifactError(f"corporate action {source_event_id!r} has bad revision")
        available_text, available_at = _canonical_timestamp(
            action.get("available_at"), field="action.available_at"
        )
        if available_at > decision_at:
            raise AdjustmentArtifactError(f"corporate action {source_event_id!r} is not knowable")
        effective_text, effective_at = _canonical_timestamp(
            action.get("effective_at"), field="action.effective_at"
        )
        if effective_at > decision_at:
            continue
        if action.get("action_status") == "cancelled":
            raise AdjustmentArtifactError("resolved action snapshot must omit cancellations")
        if action.get("observed_precision") != "date":
            raise AdjustmentArtifactError("policy 1.0.0 requires date-precision action dates")
        observed_text, observed_at = _canonical_timestamp(
            action.get("observed_at"), field="action.observed_at"
        )
        if observed_at > decision_at:
            continue
        kind = action.get("action_type")
        if action.get("action_status") != "active" or kind not in {
            "split",
            "reverse_split",
            "cash_dividend",
        }:
            raise AdjustmentArtifactError(
                f"unsupported corporate action {source_event_id!r} blocks adjustment"
            )
        if kind == "cash_dividend" and action.get("currency") != currency:
            raise AdjustmentArtifactError(f"cash dividend {source_event_id!r} currency mismatch")
        action["observed_at"] = observed_text
        action["available_at"] = available_text
        action["effective_at"] = effective_text
        record_sha = _sha256_bytes(_canonical_json(action))
        pins.append(
            {
                "id": _canonical_uuid(action.get("id"), field="action.id"),
                "source_event_id": source_event_id,
                "revision": revision,
                "record_sha256": record_sha,
                "observed_at": observed_text,
                "available_at": available_text,
                "effective_at": effective_text,
            }
        )
        selected.append(action)
    ordering = lambda action: (action["observed_at"], action["source_event_id"])
    selected.sort(key=ordering)
    pins.sort(key=ordering)
    return selected, pins


def _adjust_rows(
    raw_rows: list[dict[str, Any]], actions: list[dict[str, Any]]
) -> list[dict[str, str | bool | None]]:
    factors: dict[str, tuple[Fraction, Fraction]] = {
        row["observed_at"]: (Fraction(1), Fraction(1)) for row in raw_rows
    }
    by_date = {row["observed_at"][:10]: row for row in raw_rows}
    for action in sorted(
        actions,
        key=lambda row: (row["observed_at"], row["source_event_id"]),
        reverse=True,
    ):
        action_date = action["observed_at"][:10]
        kind = action["action_type"]
        if kind in {"split", "reverse_split"}:
            post = _fraction(action.get("ratio_numerator"), field="ratio_numerator", positive=True)
            pre = _fraction(action.get("ratio_denominator"), field="ratio_denominator", positive=True)
            price_factor, volume_factor = pre / post, post / pre
        else:
            prior = [row for date, row in by_date.items() if date < action_date]
            if not prior:
                raise AdjustmentArtifactError(
                    f"cash dividend {action['source_event_id']!r} has no prior raw close"
                )
            previous = max(prior, key=lambda row: row["observed_at"])
            close = _fraction(previous["close"], field="prior close", positive=True)
            amount = _fraction(action.get("cash_amount"), field="cash_amount")
            if amount >= close:
                raise AdjustmentArtifactError(
                    f"cash dividend {action['source_event_id']!r} is not below prior close"
                )
            price_factor, volume_factor = (close - amount) / close, Fraction(1)
        for timestamp, current in factors.items():
            if timestamp[:10] < action_date:
                factors[timestamp] = (
                    current[0] * price_factor,
                    current[1] * volume_factor,
                )

    result: list[dict[str, str | bool | None]] = []
    for row in raw_rows:
        price_factor, volume_factor = factors[row["observed_at"]]
        adjusted: dict[str, str | bool | None] = {
            "observed_at": row["observed_at"],
            "available_at": row["available_at"],
            "raw_open": row["open"],
            "raw_high": row["high"],
            "raw_low": row["low"],
            "raw_close": row["close"],
            "raw_volume": row["volume"] if row["has_volume"] else None,
            "has_volume": bool(row["has_volume"]),
            "price_factor": _canonical_fraction(price_factor),
            "volume_factor": _canonical_fraction(volume_factor),
        }
        for field in ("open", "high", "low", "close"):
            adjusted[f"adjusted_{field}"] = _canonical_fraction(
                _fraction(row[field], field=f"raw {field}") * price_factor
            )
        adjusted["adjusted_volume"] = (
            _canonical_fraction(_fraction(row["volume"], field="raw volume") * volume_factor)
            if row["has_volume"]
            else None
        )
        result.append(adjusted)
    return result


def _parquet_bytes(rows: list[dict[str, Any]]) -> bytes:
    import pyarrow as pa
    from pyarrow import parquet

    fields = [
        pa.field("schema_version", pa.string()),
        pa.field("artifact_id", pa.string()),
        pa.field("policy_version", pa.string()),
        pa.field("security_id", pa.string()),
        pa.field("source", pa.string()),
        pa.field("interval", pa.string()),
        pa.field("currency", pa.string()),
        pa.field("decision_at", pa.string()),
        pa.field("observed_at", pa.string()),
        pa.field("available_at", pa.string()),
    ]
    fields.extend(pa.field(name, pa.string()) for name in (
        "raw_open", "raw_high", "raw_low", "raw_close", "raw_volume",
        "price_factor", "volume_factor", "adjusted_open", "adjusted_high",
        "adjusted_low", "adjusted_close", "adjusted_volume",
    ))
    fields.append(pa.field("has_volume", pa.bool_()))
    table = pa.Table.from_pylist(rows, schema=pa.schema(fields))
    sink = pa.BufferOutputStream()
    parquet.write_table(
        table,
        sink,
        compression="NONE",
        data_page_version="1.0",
        use_dictionary=False,
        write_statistics=False,
        version="2.6",
    )
    return sink.getvalue().to_pybytes()


def publish_adjusted_prices(
    raw_manifest_path: str | Path,
    actions: Iterable[Mapping[str, Any]],
    *,
    decision_at: str | datetime,
    adjustments_root: str | Path = "data/adjusted/prices",
    created_at: str | datetime | None = None,
) -> Path:
    """Publish one immutable adjusted-price artifact without touching raw prices."""

    decision_text, decision_time = _canonical_timestamp(decision_at, field="decision_at")
    manifest_path = Path(raw_manifest_path).expanduser().resolve()
    raw_manifest, raw_part_path, raw_manifest_sha, raw_part_sha = _validate_raw_manifest(
        manifest_path
    )
    security_id = _canonical_uuid(
        raw_manifest["partition"].get("security_id"), field="partition.security_id"
    )
    raw_rows, source, interval, currency = _read_raw_rows(
        raw_part_path,
        security_id=security_id,
        decision_at=decision_time,
        expected_row_count=raw_manifest["row_count"],
    )
    selected_actions, action_pins = _select_actions(
        actions,
        security_id=security_id,
        currency=currency,
        decision_at=decision_time,
    )
    action_snapshot_sha = _sha256_bytes(_canonical_json(action_pins))
    identity = {
        "policy_version": POLICY_VERSION,
        "security_id": security_id,
        "decision_at": decision_text,
        "raw_price_manifest_sha256": raw_manifest_sha,
        "raw_price_part_sha256": raw_part_sha,
        "corporate_action_snapshot_sha256": action_snapshot_sha,
    }
    artifact_id = str(uuid5(_ARTIFACT_NAMESPACE, _sha256_bytes(_canonical_json(identity))))
    output_rows = _adjust_rows(raw_rows, selected_actions)
    for row in output_rows:
        row.update(
            schema_version=SCHEMA_VERSION,
            artifact_id=artifact_id,
            policy_version=POLICY_VERSION,
            security_id=security_id,
            source=source,
            interval=interval,
            currency=currency,
            decision_at=decision_text,
        )
    part_bytes = _parquet_bytes(output_rows)
    output_sha = _sha256_bytes(part_bytes)
    output_name = f"part-{output_sha}.parquet"
    created_text = _canonical_timestamp(
        created_at if created_at is not None else decision_text,
        field="created_at",
    )[0]
    manifest = {
        "schema_version": SCHEMA_VERSION,
        "manifest_version": MANIFEST_VERSION,
        "artifact_id": artifact_id,
        "policy_version": POLICY_VERSION,
        "generator_version": GENERATOR_VERSION,
        "security_id": security_id,
        "source": source,
        "interval": interval,
        "currency": currency,
        "decision_at": decision_text,
        "created_at": created_text,
        "raw_price_manifest_path": str(manifest_path),
        "raw_price_manifest_sha256": raw_manifest_sha,
        "raw_price_part_path": str(raw_part_path),
        "raw_price_part_sha256": raw_part_sha,
        "raw_price_basis": "raw",
        "corporate_action_snapshot_sha256": action_snapshot_sha,
        "selected_actions": action_pins,
        "row_count": len(output_rows),
        "parts": [{"path": output_name, "sha256": output_sha, "row_count": len(output_rows)}],
    }
    directory = Path(adjustments_root).expanduser().resolve() / f"artifact_id={artifact_id}"
    output_manifest = directory / "manifest.json"
    manifest_bytes = (json.dumps(manifest, indent=2, allow_nan=False) + "\n").encode()
    if directory.exists():
        if output_manifest.is_file() and output_manifest.read_bytes() == manifest_bytes:
            validate_adjustment_artifact(output_manifest)
            return output_manifest
        raise AdjustmentArtifactConflictError(f"immutable adjustment artifact conflict: {artifact_id}")
    directory.mkdir(parents=True, exist_ok=False)
    try:
        (directory / output_name).write_bytes(part_bytes)
        output_manifest.write_bytes(manifest_bytes)
    except OSError:
        for path in directory.iterdir():
            path.unlink()
        directory.rmdir()
        raise
    return output_manifest


def validate_adjustment_artifact(path: str | Path) -> ValidatedAdjustmentArtifact:
    """Validate one manifest and exactly its content-addressed output part."""

    manifest_path = Path(path).expanduser().resolve()
    manifest = _read_json(manifest_path, label="adjustment manifest")
    if set(manifest) != _ADJUSTMENT_MANIFEST_FIELDS:
        raise AdjustmentArtifactValidationError("adjustment manifest has unsupported fields")
    if (
        manifest.get("schema_version") != SCHEMA_VERSION
        or manifest.get("manifest_version") != MANIFEST_VERSION
        or manifest.get("policy_version") != POLICY_VERSION
        or manifest.get("generator_version") != GENERATOR_VERSION
        or manifest.get("raw_price_basis") != "raw"
    ):
        raise AdjustmentArtifactValidationError("unsupported adjustment policy or price basis")
    artifact_id = _canonical_uuid(manifest.get("artifact_id"), field="artifact_id")
    security_id = _canonical_uuid(manifest.get("security_id"), field="security_id")
    decision_at = _canonical_timestamp(manifest.get("decision_at"), field="decision_at")[0]
    _canonical_timestamp(manifest.get("created_at"), field="created_at")
    if not isinstance(manifest.get("selected_actions"), list):
        raise AdjustmentArtifactValidationError("selected_actions must be a list")
    previous_order: tuple[str, str] | None = None
    for index, pin in enumerate(manifest["selected_actions"]):
        if not isinstance(pin, dict) or set(pin) != set(_ACTION_PIN_FIELDS):
            raise AdjustmentArtifactValidationError(f"selected_actions[{index}] is invalid")
        _canonical_uuid(pin["id"], field=f"selected_actions[{index}].id")
        if (
            not isinstance(pin["revision"], int)
            or isinstance(pin["revision"], bool)
            or pin["revision"] < 0
            or not isinstance(pin["source_event_id"], str)
            or not pin["source_event_id"]
            or not isinstance(pin["record_sha256"], str)
            or _SHA256.fullmatch(pin["record_sha256"]) is None
        ):
            raise AdjustmentArtifactValidationError(f"selected_actions[{index}] values are invalid")
        observed_at = _canonical_timestamp(
            pin["observed_at"], field=f"selected_actions[{index}].observed_at"
        )[0]
        _canonical_timestamp(
            pin["available_at"], field=f"selected_actions[{index}].available_at"
        )
        _canonical_timestamp(
            pin["effective_at"], field=f"selected_actions[{index}].effective_at"
        )
        order = (observed_at, pin["source_event_id"])
        if previous_order is not None and order <= previous_order:
            raise AdjustmentArtifactValidationError("selected_actions are not uniquely ordered")
        previous_order = order
    if _sha256_bytes(_canonical_json(manifest["selected_actions"])) != manifest.get(
        "corporate_action_snapshot_sha256"
    ):
        raise AdjustmentArtifactValidationError("corporate action snapshot hash mismatch")
    raw_manifest = Path(manifest["raw_price_manifest_path"])
    raw_part = Path(manifest["raw_price_part_path"])
    if _sha256_file(raw_manifest) != manifest.get("raw_price_manifest_sha256"):
        raise AdjustmentArtifactValidationError("raw price manifest hash mismatch")
    if _sha256_file(raw_part) != manifest.get("raw_price_part_sha256"):
        raise AdjustmentArtifactValidationError("raw price part hash mismatch")
    identity = {
        "policy_version": POLICY_VERSION,
        "security_id": security_id,
        "decision_at": decision_at,
        "raw_price_manifest_sha256": manifest["raw_price_manifest_sha256"],
        "raw_price_part_sha256": manifest["raw_price_part_sha256"],
        "corporate_action_snapshot_sha256": manifest["corporate_action_snapshot_sha256"],
    }
    expected_artifact_id = str(
        uuid5(_ARTIFACT_NAMESPACE, _sha256_bytes(_canonical_json(identity)))
    )
    if artifact_id != expected_artifact_id:
        raise AdjustmentArtifactValidationError("adjustment artifact_id mismatch")
    parts = manifest.get("parts")
    if not isinstance(parts, list) or len(parts) != 1:
        raise AdjustmentArtifactValidationError("adjustment manifest requires one part")
    item = parts[0]
    if not isinstance(item, dict) or set(item) != _NORMALIZED_PART_FIELDS:
        raise AdjustmentArtifactValidationError("adjustment output part is invalid")
    if (
        not isinstance(manifest.get("row_count"), int)
        or isinstance(manifest["row_count"], bool)
        or manifest["row_count"] < 0
        or not isinstance(item.get("row_count"), int)
        or isinstance(item["row_count"], bool)
        or item["row_count"] != manifest["row_count"]
    ):
        raise AdjustmentArtifactValidationError("adjustment row_count is invalid")
    name = item.get("path") if isinstance(item, dict) else None
    sha256 = item.get("sha256") if isinstance(item, dict) else None
    match = _PART.fullmatch(name) if isinstance(name, str) else None
    if match is None or match.group(1) != sha256:
        raise AdjustmentArtifactValidationError("adjustment part is not content-named")
    part_path = manifest_path.parent / name
    if _sha256_file(part_path) != sha256:
        raise AdjustmentArtifactValidationError("adjustment output part hash mismatch")
    unexpected = {item.name for item in manifest_path.parent.iterdir()} - {"manifest.json", name}
    if unexpected:
        raise AdjustmentArtifactValidationError("adjustment artifact contains unlisted files")
    from pyarrow import parquet

    rows = parquet.ParquetFile(part_path).read().to_pylist()
    if len(rows) != manifest.get("row_count") or len(rows) != item.get("row_count"):
        raise AdjustmentArtifactValidationError("adjustment row_count mismatch")
    previous_timestamp: str | None = None
    for index, row in enumerate(rows):
        if set(row) != _OUTPUT_FIELDS:
            raise AdjustmentArtifactValidationError(f"adjustment row {index} has unknown fields")
        for field in (
            "schema_version",
            "artifact_id",
            "policy_version",
            "security_id",
            "source",
            "interval",
            "currency",
            "decision_at",
        ):
            if row[field] != manifest[field]:
                raise AdjustmentArtifactValidationError(
                    f"adjustment row {index} {field} disagrees with manifest"
                )
        observed_at = _canonical_timestamp(row["observed_at"], field="row.observed_at")[0]
        _canonical_timestamp(row["available_at"], field="row.available_at")
        if previous_timestamp is not None and observed_at <= previous_timestamp:
            raise AdjustmentArtifactValidationError("adjustment rows are not uniquely ordered")
        previous_timestamp = observed_at
        decimal_fields = (
            "raw_open",
            "raw_high",
            "raw_low",
            "raw_close",
            "price_factor",
            "volume_factor",
            "adjusted_open",
            "adjusted_high",
            "adjusted_low",
            "adjusted_close",
        )
        for field in decimal_fields:
            _fraction(row[field], field=f"row.{field}")
        if row["has_volume"]:
            _fraction(row["raw_volume"], field="row.raw_volume")
            _fraction(row["adjusted_volume"], field="row.adjusted_volume")
        elif row["raw_volume"] is not None or row["adjusted_volume"] is not None:
            raise AdjustmentArtifactValidationError("volume presence flags disagree")
    return ValidatedAdjustmentArtifact(manifest_path, manifest, tuple(rows), part_path)


__all__ = [
    "GENERATOR_VERSION",
    "MANIFEST_VERSION",
    "POLICY_VERSION",
    "SCHEMA_VERSION",
    "AdjustmentArtifactConflictError",
    "AdjustmentArtifactError",
    "AdjustmentArtifactValidationError",
    "ValidatedAdjustmentArtifact",
    "publish_adjusted_prices",
    "validate_adjustment_artifact",
]
