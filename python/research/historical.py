"""Pure point-in-time resolvers for the v0.2 historical truth contracts.

The functions in this module operate on already validated canonical records. They
deliberately do not read the current YAML universe, fetch providers, or infer
missing sessions. PostgreSQL publication and database constraints will use the same
selection rules when the durable historical tables are added.
"""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Callable, Iterable, Mapping
from datetime import UTC, datetime
from typing import Any

type Record = Mapping[str, Any]
_UTC_TIMESTAMP = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$")
_CALENDAR_FINGERPRINT_FIELDS = (
    "mic",
    "exchange_timezone",
    "session_date",
    "session_status",
    "open_at",
    "close_at",
    "is_early_close",
)


class HistoricalResolutionError(ValueError):
    """Raised when a historical contract cannot produce a safe answer."""


class AmbiguousHistoricalResolution(HistoricalResolutionError):
    """Raised when equal-ranked eligible records disagree."""


def parse_utc(value: str | datetime, *, field: str = "timestamp") -> datetime:
    """Parse a canonical UTC timestamp without accepting naive wall-clock values."""

    if isinstance(value, datetime):
        parsed = value
    elif isinstance(value, str) and _UTC_TIMESTAMP.fullmatch(value):
        parsed = datetime.fromisoformat(value)
    else:
        raise HistoricalResolutionError(f"{field} must be an RFC 3339 UTC timestamp ending in Z")
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise HistoricalResolutionError(f"{field} must include an explicit timezone")
    return parsed.astimezone(UTC)


def _interval_contains(row: Record, as_of: datetime) -> bool:
    start = parse_utc(row["valid_from"], field="valid_from")
    end_value = row.get("valid_until")
    if end_value is None:
        return start <= as_of
    end = parse_utc(end_value, field="valid_until")
    if end <= start:
        raise HistoricalResolutionError("valid_until must be after valid_from")
    return start <= as_of < end


def _available_by(row: Record, decision_at: datetime) -> bool:
    return parse_utc(row["available_at"], field="available_at") <= decision_at


def _eligible(row: Record, *, as_of: datetime, decision_at: datetime) -> bool:
    return _available_by(row, decision_at) and _interval_contains(row, as_of)


def _selection_rank(row: Record) -> tuple[datetime, int, datetime]:
    try:
        revision = int(row.get("revision", 0))
    except (TypeError, ValueError) as error:
        raise HistoricalResolutionError("revision must be a non-negative integer") from error
    if revision < 0:
        raise HistoricalResolutionError("revision must be a non-negative integer")
    return (
        parse_utc(row["available_at"], field="available_at"),
        revision,
        parse_utc(row["recorded_at"], field="recorded_at"),
    )


def _select_latest(
    rows: Iterable[Record],
    *,
    identity_key: Callable[[Record], tuple[Any, ...]],
    label: str,
) -> Record | None:
    candidates = tuple(rows)
    if not candidates:
        return None
    highest_rank = max(_selection_rank(row) for row in candidates)
    leaders = tuple(row for row in candidates if _selection_rank(row) == highest_rank)
    identities = {identity_key(row) for row in leaders}
    if len(identities) > 1:
        raise AmbiguousHistoricalResolution(
            f"{label} has equal-ranked records with conflicting identities"
        )
    return max(leaders, key=lambda row: str(row.get("id", "")))


def _collapse_revisions(
    rows: Iterable[Record],
    *,
    family_key: Callable[[Record], tuple[Any, ...]],
    assertion_key: Callable[[Record], tuple[Any, ...]],
    label: str,
) -> tuple[Record, ...]:
    """Select the latest knowable correction for each source assertion family."""

    families: dict[tuple[Any, ...], list[Record]] = {}
    for row in rows:
        families.setdefault(family_key(row), []).append(row)
    selected = []
    for family, versions in families.items():
        latest = _select_latest(
            versions,
            identity_key=assertion_key,
            label=f"{label} correction {family!r}",
        )
        if latest is not None:
            selected.append(latest)
    return tuple(selected)


def security_identifier_as_of(
    rows: Iterable[Record],
    *,
    identifier_type: str,
    value: str,
    identifier_scope: str,
    as_of: str | datetime,
    decision_at: str | datetime,
) -> Record | None:
    """Resolve one scoped identifier using both market-time and knowledge cutoffs."""

    world_time = parse_utc(as_of, field="as_of")
    decision_time = parse_utc(decision_at, field="decision_at")
    normalized = value.strip().upper()
    kind = identifier_type.strip().lower()
    scope = identifier_scope.strip().upper()
    known = tuple(
        row
        for row in rows
        if str(row.get("identifier_type", "")).lower() == kind
        and str(row.get("normalized_value", "")).upper() == normalized
        and str(row.get("identifier_scope", "")).upper() == scope
        and _available_by(row, decision_time)
    )
    revisions = _collapse_revisions(
        known,
        family_key=lambda row: (
            row.get("security_id"),
            str(row.get("identifier_type", "")).lower(),
            str(row.get("normalized_value", "")).upper(),
            str(row.get("identifier_scope", "")).upper(),
            row.get("valid_from"),
        ),
        assertion_key=lambda row: (
            row.get("security_id"),
            row.get("value"),
            row.get("normalized_value"),
            row.get("identifier_scope"),
            row.get("valid_from"),
            row.get("valid_until"),
            row.get("is_primary"),
        ),
        label=f"{kind} {normalized!r} in {scope}",
    )
    matches = (row for row in revisions if _interval_contains(row, world_time))
    return _select_latest(
        matches,
        identity_key=lambda row: (row.get("security_id"),),
        label=f"{kind} {normalized!r} in {scope}",
    )


def listing_as_of(
    rows: Iterable[Record],
    *,
    security_id: str,
    mic: str,
    as_of: str | datetime,
    decision_at: str | datetime,
) -> Record | None:
    """Resolve one security listing at a MIC and explicit historical cutoff."""

    world_time = parse_utc(as_of, field="as_of")
    decision_time = parse_utc(decision_at, field="decision_at")
    requested_mic = mic.strip().upper()
    known = (
        row
        for row in rows
        if row.get("security_id") == security_id
        and str(row.get("mic", "")).upper() == requested_mic
        and _available_by(row, decision_time)
    )
    revisions = _collapse_revisions(
        known,
        family_key=lambda row: (
            row.get("security_id"),
            str(row.get("mic", "")).upper(),
            row.get("valid_from"),
        ),
        assertion_key=lambda row: (
            row.get("issuer_id"),
            row.get("exchange"),
            row.get("mic"),
            row.get("currency"),
            row.get("primary_listing"),
            row.get("valid_from"),
            row.get("valid_until"),
        ),
        label=f"listing {security_id} on {requested_mic}",
    )
    matches = (row for row in revisions if _interval_contains(row, world_time))
    return _select_latest(
        matches,
        identity_key=lambda row: (
            row.get("issuer_id"),
            row.get("currency"),
            row.get("primary_listing"),
        ),
        label=f"listing {security_id} on {requested_mic}",
    )


def listings_as_of(
    rows: Iterable[Record],
    *,
    security_id: str,
    as_of: str | datetime,
    decision_at: str | datetime,
) -> tuple[Record, ...]:
    """Resolve all MIC-scoped listings for a security at one historical cutoff."""

    candidate_rows = tuple(rows)
    mics = sorted(
        {
            str(row.get("mic", "")).upper()
            for row in candidate_rows
            if row.get("security_id") == security_id
        }
    )
    resolved = tuple(
        listing
        for requested_mic in mics
        if (listing := listing_as_of(
            candidate_rows,
            security_id=security_id,
            mic=requested_mic,
            as_of=as_of,
            decision_at=decision_at,
        ))
        is not None
    )
    return tuple(sorted(resolved, key=lambda row: (str(row.get("mic")), str(row.get("id")))))


def membership_as_of(
    rows: Iterable[Record],
    *,
    universe_id: str,
    security_id: str,
    as_of: str | datetime,
    decision_at: str | datetime,
) -> Record | None:
    """Resolve the latest eligible membership assertion for one security."""

    world_time = parse_utc(as_of, field="as_of")
    decision_time = parse_utc(decision_at, field="decision_at")
    known = (
        row
        for row in rows
        if row.get("universe_id") == universe_id
        and row.get("security_id") == security_id
        and _available_by(row, decision_time)
    )
    revisions = _collapse_revisions(
        known,
        family_key=lambda row: (
            row.get("universe_id"),
            row.get("security_id"),
            row.get("valid_from"),
        ),
        assertion_key=lambda row: (
            row.get("universe_id"),
            row.get("security_id"),
            row.get("member"),
            row.get("valid_from"),
            row.get("valid_until"),
        ),
        label=f"membership {universe_id}/{security_id}",
    )
    matches = (row for row in revisions if _interval_contains(row, world_time))
    return _select_latest(
        matches,
        identity_key=lambda row: (row.get("member"),),
        label=f"membership {universe_id}/{security_id}",
    )


def universe_as_of(
    rows: Iterable[Record],
    *,
    universe_id: str,
    as_of: str | datetime,
    decision_at: str | datetime,
) -> tuple[str, ...]:
    """Return member security IDs without consulting the current universe config."""

    candidate_rows = tuple(row for row in rows if row.get("universe_id") == universe_id)
    security_ids = sorted({str(row["security_id"]) for row in candidate_rows})
    members = []
    for security_id in security_ids:
        assertion = membership_as_of(
            candidate_rows,
            universe_id=universe_id,
            security_id=security_id,
            as_of=as_of,
            decision_at=decision_at,
        )
        if assertion is not None and assertion.get("member") is True:
            members.append(security_id)
    return tuple(members)


def _calendar_row_matches(
    row: Record,
    *,
    mic: str,
    calendar_version: str,
    decision_time: datetime,
) -> bool:
    return (
        str(row.get("mic", "")).upper() == mic.strip().upper()
        and row.get("calendar_version") == calendar_version
        and row.get("session_status") == "open"
        and row.get("open_at") is not None
        and row.get("close_at") is not None
        and _available_by(row, decision_time)
    )


def calendar_manifest_as_of(
    rows: Iterable[Record],
    *,
    data_source_id: str,
    mic: str,
    decision_at: str | datetime,
) -> Record | None:
    """Select the latest explicit calendar version knowable at ``decision_at``."""

    decision_time = parse_utc(decision_at, field="decision_at")
    requested_mic = mic.strip().upper()
    known = tuple(
        row
        for row in rows
        if row.get("data_source_id") == data_source_id
        and str(row.get("mic", "")).upper() == requested_mic
        and _available_by(row, decision_time)
    )
    if not known:
        return None
    highest_rank = max(
        (
            parse_utc(row["available_at"], field="available_at"),
            parse_utc(row["recorded_at"], field="recorded_at"),
        )
        for row in known
    )
    leaders = tuple(
        row
        for row in known
        if (
            parse_utc(row["available_at"], field="available_at"),
            parse_utc(row["recorded_at"], field="recorded_at"),
        )
        == highest_rank
    )
    identities = {
        (
            row.get("id"),
            row.get("schema_version"),
            row.get("calendar_version"),
            row.get("mic"),
            row.get("exchange_timezone"),
            row.get("source_reference"),
            row.get("data_source_id"),
            row.get("raw_payload_hash"),
            row.get("session_fingerprint"),
            row.get("session_count"),
        )
        for row in leaders
    }
    if len(identities) > 1:
        raise AmbiguousHistoricalResolution(
            f"calendar manifest {data_source_id}/{requested_mic} has equal-ranked "
            "records with conflicting identities"
        )
    return max(leaders, key=lambda row: str(row.get("id", "")))


def corporate_actions_as_of(
    rows: Iterable[Record],
    *,
    data_source_id: str,
    security_id: str,
    decision_at: str | datetime,
) -> tuple[Record, ...]:
    """Select the latest knowable version of every corporate-action family."""

    decision_time = parse_utc(decision_at, field="decision_at")

    def provenance_value(row: Record, field: str) -> Any:
        provenance = row.get("provenance")
        if isinstance(provenance, Mapping):
            return provenance.get(field)
        return row.get(field)

    known = (
        row
        for row in rows
        if provenance_value(row, "data_source_id") == data_source_id
        and row.get("security_id") == security_id
        and _available_by(row, decision_time)
    )
    selected = _collapse_revisions(
        known,
        family_key=lambda row: (row.get("source_event_id"),),
        assertion_key=lambda row: (
            row.get("security_id"),
            row.get("source_event_id"),
            row.get("action_status"),
            row.get("action_type"),
            row.get("observed_at"),
            row.get("observed_precision"),
            row.get("published_at"),
            row.get("published_precision"),
            row.get("effective_at"),
            row.get("effective_precision"),
            row.get("record_date"),
            row.get("payment_date"),
            row.get("ratio_numerator"),
            row.get("ratio_denominator"),
            row.get("cash_amount"),
            row.get("currency"),
            row.get("target_security_id"),
            row.get("source_reference"),
            provenance_value(row, "raw_record_locator"),
            provenance_value(row, "data_source_id"),
            provenance_value(row, "ingestion_run_id"),
            provenance_value(row, "raw_payload_hash"),
            provenance_value(row, "ingested_at"),
            provenance_value(row, "normalizer_version"),
        ),
        label="corporate action",
    )
    active: list[Record] = []
    for row in selected:
        status = row.get("action_status")
        if status == "cancelled":
            continue
        if status not in {"active", "unsupported"}:
            raise HistoricalResolutionError(f"unsupported corporate action status {status!r}")
        active.append(row)
    return tuple(
        sorted(
            active,
            key=lambda row: (
                parse_utc(row["observed_at"], field="observed_at"),
                str(row.get("source_event_id", "")),
            ),
        )
    )


def trading_session_at(
    rows: Iterable[Record],
    *,
    mic: str,
    calendar_version: str,
    timestamp: str | datetime,
    decision_at: str | datetime,
) -> Record | None:
    """Find the explicit open session containing ``timestamp``."""

    moment = parse_utc(timestamp, field="timestamp")
    decision_time = parse_utc(decision_at, field="decision_at")
    matches = (
        row
        for row in rows
        if _calendar_row_matches(
            row,
            mic=mic,
            calendar_version=calendar_version,
            decision_time=decision_time,
        )
        and parse_utc(row["open_at"], field="open_at")
        <= moment
        < parse_utc(row["close_at"], field="close_at")
    )
    return _select_latest(
        matches,
        identity_key=lambda row: (
            row.get("session_status"),
            row.get("open_at"),
            row.get("close_at"),
            row.get("is_early_close"),
        ),
        label=f"session {mic}/{calendar_version} at {moment.isoformat()}",
    )


def next_trading_session(
    rows: Iterable[Record],
    *,
    mic: str,
    calendar_version: str,
    after: str | datetime,
    decision_at: str | datetime,
) -> Record | None:
    """Find the first explicit open session after an instant."""

    after_time = parse_utc(after, field="after")
    decision_time = parse_utc(decision_at, field="decision_at")
    matches = (
        row
        for row in rows
        if _calendar_row_matches(
            row,
            mic=mic,
            calendar_version=calendar_version,
            decision_time=decision_time,
        )
        and parse_utc(row["open_at"], field="open_at") > after_time
    )
    by_date: dict[str, list[Record]] = {}
    for row in matches:
        by_date.setdefault(str(row["session_date"]), []).append(row)
    resolved = tuple(
        selected
        for date_rows in by_date.values()
        if (selected := _select_latest(
            date_rows,
            identity_key=lambda row: (
                row.get("session_status"),
                row.get("open_at"),
                row.get("close_at"),
                row.get("is_early_close"),
            ),
            label=f"session {mic}/{calendar_version}",
        ))
        is not None
    )
    return min(resolved, key=lambda row: parse_utc(row["open_at"], field="open_at"), default=None)


def after_close_execution_session(
    rows: Iterable[Record],
    *,
    mic: str,
    calendar_version: str,
    decision_at: str | datetime,
) -> Record | None:
    """Apply the bounded ``after_close_next_session`` decision clock."""

    decision_time = parse_utc(decision_at, field="decision_at")
    session = trading_session_at(
        rows,
        mic=mic,
        calendar_version=calendar_version,
        timestamp=decision_time,
        decision_at=decision_time,
    )
    if session is not None:
        raise HistoricalResolutionError(
            "after_close_next_session requires decision_at at or after the session close"
        )
    return next_trading_session(
        rows,
        mic=mic,
        calendar_version=calendar_version,
        after=decision_time,
        decision_at=decision_time,
    )


def calendar_fingerprint(
    rows: Iterable[Record], *, mic: str, calendar_version: str
) -> str:
    """Hash explicit session definitions, excluding mutable provenance metadata."""

    selected = [
        {
            field: row[field]
            for field in _CALENDAR_FINGERPRINT_FIELDS
        }
        for row in rows
        if str(row.get("mic", "")).upper() == mic.strip().upper()
        and row.get("calendar_version") == calendar_version
    ]
    selected.sort(key=lambda row: tuple(str(row[field]) for field in _CALENDAR_FINGERPRINT_FIELDS))
    canonical = json.dumps(
        selected,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")
    return hashlib.sha256(canonical).hexdigest()


__all__ = [
    "AmbiguousHistoricalResolution",
    "HistoricalResolutionError",
    "after_close_execution_session",
    "calendar_fingerprint",
    "calendar_manifest_as_of",
    "corporate_actions_as_of",
    "listing_as_of",
    "listings_as_of",
    "membership_as_of",
    "next_trading_session",
    "parse_utc",
    "security_identifier_as_of",
    "trading_session_at",
    "universe_as_of",
]
