"""Point-in-time PTAX selection and deterministic USD/BRL conversion."""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Iterable, Mapping
from datetime import UTC, date, datetime
from decimal import ROUND_HALF_EVEN, Decimal, localcontext
from fractions import Fraction
from typing import Any, Final
from uuid import UUID
from zoneinfo import ZoneInfo

POLICY_VERSION: Final[str] = "ptax_sale_direct_inverse_1_0_0"
NON_TERMINATING_SIGNIFICANT_DIGITS: Final[int] = 50

_DECIMAL = re.compile(r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$")
_UNSIGNED_DECIMAL = re.compile(r"^(0|[1-9][0-9]*)(\.[0-9]+)?$")
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_UTC_TIMESTAMP = re.compile(
    r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$"
)
_RECORD_FIELDS: Final[tuple[str, ...]] = (
    "schema_version",
    "id",
    "source",
    "base_currency",
    "quote_currency",
    "rate_kind",
    "fixing_timezone",
    "buy_rate",
    "sell_rate",
    "fixing_at",
    "published_at",
    "available_at",
    "recorded_at",
    "revision",
    "source_record_id",
    "raw_payload_hash",
    "data_source_id",
    "ingestion_run_id",
    "raw_record_locator",
    "normalizer_version",
)


class FXPolicyError(ValueError):
    """Raised when an FX observation or conversion violates policy 1.0.0."""


class FXObservationNotFound(FXPolicyError):
    """Raised when the requested fixing was not knowable at the decision time."""


class AmbiguousFXObservation(FXPolicyError):
    """Raised when more than one canonical row claims the selected fixing."""


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
            raise FXPolicyError(f"{field} must be RFC 3339 with a timezone") from error
    else:
        raise FXPolicyError(f"{field} must be RFC 3339 with a timezone")
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise FXPolicyError(f"{field} must include a timezone")
    utc = parsed.astimezone(UTC)
    fraction = f".{utc.microsecond:06d}".rstrip("0") if utc.microsecond else ""
    canonical = utc.strftime("%Y-%m-%dT%H:%M:%S") + fraction + "Z"
    if not _UTC_TIMESTAMP.fullmatch(canonical):
        raise FXPolicyError(f"{field} is outside the timestamp contract")
    return canonical, utc


def _canonical_uuid(value: Any, *, field: str) -> str:
    try:
        canonical = str(UUID(str(value)))
    except (TypeError, ValueError) as error:
        raise FXPolicyError(f"{field} must be a UUID") from error
    if value != canonical:
        raise FXPolicyError(f"{field} must be a canonical UUID")
    return canonical


def _canonical_decimal(value: Any, *, field: str, positive: bool = False) -> str:
    pattern = _UNSIGNED_DECIMAL if positive else _DECIMAL
    if not isinstance(value, str) or not pattern.fullmatch(value):
        raise FXPolicyError(f"{field} must be a canonical decimal string")
    if value == "-0" or "." in value and value.endswith("0"):
        raise FXPolicyError(f"{field} must be a canonical decimal string")
    if positive and Decimal(value) <= 0:
        raise FXPolicyError(f"{field} must be positive")
    return value


def canonical_fx_observation(observation: Mapping[str, Any]) -> dict[str, Any]:
    """Validate and normalize one flattened canonical FX Parquet row."""
    missing = [field for field in _RECORD_FIELDS if field not in observation]
    if missing:
        raise FXPolicyError(f"FX observation is missing fields: {', '.join(missing)}")
    record = {field: observation[field] for field in _RECORD_FIELDS}
    if record["schema_version"] != "1.0.0":
        raise FXPolicyError("unsupported FX schema_version")
    _canonical_uuid(record["id"], field="id")
    _canonical_uuid(record["data_source_id"], field="data_source_id")
    _canonical_uuid(record["ingestion_run_id"], field="ingestion_run_id")
    if (
        record["source"] != "bcb_ptax"
        or record["base_currency"] != "USD"
        or record["quote_currency"] != "BRL"
        or record["rate_kind"] != "ptax_closing"
        or record["fixing_timezone"] != "America/Sao_Paulo"
        or record["revision"] != 0
    ):
        raise FXPolicyError("unsupported FX source contract")
    buy = _canonical_decimal(record["buy_rate"], field="buy_rate", positive=True)
    sell = _canonical_decimal(record["sell_rate"], field="sell_rate", positive=True)
    if Decimal(buy) > Decimal(sell):
        raise FXPolicyError("buy_rate must not exceed sell_rate")
    times = {
        field: _canonical_timestamp(record[field], field=field)
        for field in ("fixing_at", "published_at", "available_at", "recorded_at")
    }
    if not (times["fixing_at"][1] == times["published_at"][1] == times["available_at"][1]):
        raise FXPolicyError("fixing, publication, and availability timestamps must match")
    if times["recorded_at"][1] < times["available_at"][1]:
        raise FXPolicyError("recorded_at precedes available_at")
    for field, (canonical, _) in times.items():
        record[field] = canonical
    if not isinstance(record["source_record_id"], str) or not record["source_record_id"]:
        raise FXPolicyError("source_record_id is required")
    if not isinstance(record["raw_payload_hash"], str) or not _SHA256.fullmatch(record["raw_payload_hash"]):
        raise FXPolicyError("raw_payload_hash must be a lower-case SHA-256")
    if not isinstance(record["raw_record_locator"], str) or not record["raw_record_locator"]:
        raise FXPolicyError("raw_record_locator is required")
    if not isinstance(record["normalizer_version"], str) or not record["normalizer_version"]:
        raise FXPolicyError("normalizer_version is required")
    return record


def fx_record_hash(observation: Mapping[str, Any]) -> str:
    record = canonical_fx_observation(observation)
    encoded = json.dumps(
        record, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False
    ).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def fx_as_of(
    observations: Iterable[Mapping[str, Any]],
    *,
    pair: str,
    fixing_date: str | date,
    decision_at: str | datetime,
) -> dict[str, Any]:
    """Select the exact PTAX fixing eligible at ``decision_at`` without carry-forward."""
    if pair != "USD-BRL":
        raise FXPolicyError(f"unsupported FX pair {pair!r}")
    try:
        requested_date = (
            fixing_date if isinstance(fixing_date, date) else date.fromisoformat(fixing_date)
        )
    except (TypeError, ValueError) as error:
        raise FXPolicyError("fixing_date must be an ISO date") from error
    _, decision = _canonical_timestamp(decision_at, field="decision_at")
    timezone = ZoneInfo("America/Sao_Paulo")
    eligible: list[dict[str, Any]] = []
    for candidate in observations:
        record = canonical_fx_observation(candidate)
        _, fixing = _canonical_timestamp(record["fixing_at"], field="fixing_at")
        _, available = _canonical_timestamp(record["available_at"], field="available_at")
        if fixing.astimezone(timezone).date() == requested_date and available <= decision:
            eligible.append(record)
    if not eligible:
        raise FXObservationNotFound(
            f"no USD-BRL PTAX closing fixing for {requested_date.isoformat()} "
            f"was available at the decision time"
        )
    if len(eligible) != 1:
        raise AmbiguousFXObservation(
            f"multiple USD-BRL PTAX closing fixings resolve for {requested_date.isoformat()}"
        )
    return eligible[0]


def _canonical_fraction(value: Fraction) -> str:
    denominator = value.denominator
    twos = fives = 0
    while denominator % 2 == 0:
        denominator //= 2
        twos += 1
    while denominator % 5 == 0:
        denominator //= 5
        fives += 1
    if denominator == 1:
        scale = max(twos, fives)
        scaled = value.numerator * 2 ** (scale - twos) * 5 ** (scale - fives)
        sign = "-" if scaled < 0 else ""
        digits = str(abs(scaled)).rjust(scale + 1, "0")
        if scale == 0:
            return sign + digits
        return (sign + digits[:-scale] + "." + digits[-scale:]).rstrip("0").rstrip(".")
    with localcontext() as context:
        context.prec = NON_TERMINATING_SIGNIFICANT_DIGITS
        context.rounding = ROUND_HALF_EVEN
        result = format(Decimal(value.numerator) / Decimal(value.denominator), "f")
    return result.rstrip("0").rstrip(".")


def convert_currency(
    amount: str,
    *,
    source_currency: str,
    target_currency: str,
    observation: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    """Convert with the PTAX sell side and return the complete reproducibility pin."""
    canonical_amount = _canonical_decimal(amount, field="amount")
    if source_currency == target_currency:
        if observation is not None:
            raise FXPolicyError("same-currency conversion must not use an FX observation")
        return {
            "amount": canonical_amount,
            "source_currency": source_currency,
            "target_currency": target_currency,
            "converted_amount": canonical_amount,
            "fx_input": None,
            "policy_version": POLICY_VERSION,
        }
    if (source_currency, target_currency) not in {("USD", "BRL"), ("BRL", "USD")}:
        raise FXPolicyError(
            f"unsupported conversion {source_currency}->{target_currency}; triangulation is disabled"
        )
    if observation is None:
        raise FXPolicyError("non-identity conversion requires an FX observation")
    record = canonical_fx_observation(observation)
    value = Fraction(Decimal(canonical_amount))
    rate = Fraction(Decimal(record["sell_rate"]))
    converted = value * rate if source_currency == "USD" else value / rate
    pin_fields = (
        "id",
        "source",
        "base_currency",
        "quote_currency",
        "rate_kind",
        "revision",
        "fixing_at",
        "available_at",
        "sell_rate",
        "raw_payload_hash",
        "ingestion_run_id",
    )
    pin = {field: record[field] for field in pin_fields}
    pin["canonical_record_hash"] = fx_record_hash(record)
    pin["policy_version"] = POLICY_VERSION
    return {
        "amount": canonical_amount,
        "source_currency": source_currency,
        "target_currency": target_currency,
        "converted_amount": _canonical_fraction(converted),
        "fx_input": pin,
        "policy_version": POLICY_VERSION,
    }
