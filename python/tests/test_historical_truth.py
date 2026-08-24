from __future__ import annotations

import json
import os
from copy import deepcopy
from pathlib import Path
from uuid import UUID

import pytest

from research.historical import (
    AmbiguousHistoricalResolution,
    HistoricalResolutionError,
    after_close_execution_session,
    calendar_fingerprint,
    listing_as_of,
    next_trading_session,
    parse_utc,
    security_identifier_as_of,
    trading_session_at,
    universe_as_of,
)

ROOT = Path(__file__).resolve().parents[2]
SCHEMA_ROOT = Path(os.environ.get("INVS_SCHEMA_ROOT", str(ROOT / "schemas")))
FIXTURE = json.loads((SCHEMA_ROOT / "historical-truth.fixture.json").read_text())
CALENDAR_FIXTURE = json.loads((SCHEMA_ROOT / "calendar.fixture.json").read_text())


def _schema(name: str) -> dict:
    return json.loads((SCHEMA_ROOT / name).read_text())


def _assert_entity_shape(document: dict, schema_name: str) -> None:
    schema = _schema(schema_name)
    assert set(document) == set(schema["required"])
    assert set(document) <= set(schema["properties"])
    for name, property_schema in schema["properties"].items():
        if "const" in property_schema and name in document:
            assert document[name] == property_schema["const"]
    UUID(document["id"])
    for field in ("available_at", "recorded_at"):
        parse_utc(document[field], field=field)
    if "valid_from" in document:
        start = parse_utc(document["valid_from"], field="valid_from")
        end = document["valid_until"]
        if end is not None:
            assert start < parse_utc(end, field="valid_until")
    if "raw_payload_hash" in document:
        assert len(document["raw_payload_hash"]) == 64
        assert set(document["raw_payload_hash"]) <= set("0123456789abcdef")


def _records(section: str, key: str) -> list[dict]:
    return FIXTURE[section][key]


def test_historical_fixture_matches_strict_entity_contracts() -> None:
    for section in ("us", "brazil"):
        for key, schema_name in (
            ("security_identifiers", "security-identifier-version.schema.json"),
            ("listings", "security-listing-version.schema.json"),
            ("memberships", "universe-membership.schema.json"),
        ):
            for row in _records(section, key):
                _assert_entity_shape(row, schema_name)

    for manifest in CALENDAR_FIXTURE["manifests"]:
        _assert_entity_shape(manifest, "calendar-manifest.schema.json")
    for session in CALENDAR_FIXTURE["sessions"]:
        _assert_entity_shape(session, "trading-session.schema.json")
        if session["session_status"] == "open":
            assert session["open_at"] is not None
            assert session["close_at"] is not None
            assert parse_utc(session["open_at"]) < parse_utc(session["close_at"])
        else:
            assert session["open_at"] is None
            assert session["close_at"] is None


def test_us_identity_preserves_scope_rename_and_delisting() -> None:
    rows = _records("us", "security_identifiers")
    security_a = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
    security_b = "5b27f9b7-2c2a-4ab7-9691-7ad013a0a2b1"

    assert (
        security_identifier_as_of(
            rows,
            identifier_type="ticker",
            value="OLD",
            identifier_scope="XNAS",
            as_of="2021-06-01T00:00:00Z",
            decision_at="2021-01-01T23:59:59.999999Z",
        )
        is None
    )
    assert security_identifier_as_of(
        rows,
        identifier_type="ticker",
        value="old",
        identifier_scope="xnas",
        as_of="2021-06-01T00:00:00Z",
        decision_at="2021-01-02T00:00:00Z",
    )["security_id"] == security_a
    assert security_identifier_as_of(
        rows,
        identifier_type="ticker",
        value="NEW",
        identifier_scope="XNAS",
        as_of="2022-02-01T00:00:00Z",
        decision_at="2022-01-04T00:00:00Z",
    )["security_id"] == security_a
    assert security_identifier_as_of(
        rows,
        identifier_type="ticker",
        value="OLD",
        identifier_scope="XNYS",
        as_of="2021-06-01T00:00:00Z",
        decision_at="2021-01-02T00:00:00Z",
    )["security_id"] == security_b
    assert security_identifier_as_of(
        rows,
        identifier_type="ticker",
        value="NEW",
        identifier_scope="XNAS",
        as_of="2023-07-01T00:00:00Z",
        decision_at="2023-07-02T00:00:00Z",
    ) is None

    listing = listing_as_of(
        _records("us", "listings"),
        security_id=security_a,
        mic="XNAS",
        as_of="2022-02-01T00:00:00Z",
        decision_at="2022-01-04T00:00:00Z",
    )
    assert listing is not None
    assert listing["currency"] == "USD"
    assert listing_as_of(
        _records("us", "listings"),
        security_id=security_a,
        mic="XNAS",
        as_of="2023-07-01T00:00:00Z",
        decision_at="2023-07-02T00:00:00Z",
    ) is None


def test_membership_removal_is_knowledge_cutoff_aware() -> None:
    rows = _records("us", "memberships")
    before_removal = universe_as_of(
        rows,
        universe_id="us-benchmark",
        as_of="2023-08-01T00:00:00Z",
        decision_at="2023-07-04T23:59:59.999999Z",
    )
    at_removal_availability = universe_as_of(
        rows,
        universe_id="us-benchmark",
        as_of="2023-08-01T00:00:00Z",
        decision_at="2023-07-05T00:00:00Z",
    )

    assert "469fc20f-7d4b-45bb-b827-05f8410e71aa" in before_removal
    assert "469fc20f-7d4b-45bb-b827-05f8410e71aa" not in at_removal_availability
    assert "5b27f9b7-2c2a-4ab7-9691-7ad013a0a2b1" in at_removal_availability


def test_interval_shortening_correction_suppresses_open_ended_identity() -> None:
    identifier = deepcopy(_records("us", "security_identifiers")[1])
    identifier["valid_until"] = None
    correction = deepcopy(identifier)
    correction.update(
        id="10000000-0000-4000-8000-000000000099",
        valid_until="2023-07-01T00:00:00Z",
        available_at="2023-07-02T00:00:00Z",
        recorded_at="2023-07-02T00:00:00Z",
        revision=1,
    )
    query = {
        "identifier_type": "ticker",
        "value": "NEW",
        "identifier_scope": "XNAS",
        "as_of": "2023-08-01T00:00:00Z",
    }
    assert security_identifier_as_of(
        [identifier, correction],
        **query,
        decision_at="2023-07-01T23:59:59.999999Z",
    ) == identifier
    assert (
        security_identifier_as_of(
            [identifier, correction],
            **query,
            decision_at="2023-07-02T00:00:00Z",
        )
        is None
    )

    listing = deepcopy(_records("us", "listings")[0])
    listing["valid_until"] = None
    listing_correction = deepcopy(listing)
    listing_correction.update(
        id="10000000-0000-4000-8000-000000000199",
        valid_until="2023-07-01T00:00:00Z",
        available_at="2023-07-02T00:00:00Z",
        recorded_at="2023-07-02T00:00:00Z",
        revision=1,
    )
    assert listing_as_of(
        [listing, listing_correction],
        security_id=listing["security_id"],
        mic="XNAS",
        as_of="2023-08-01T00:00:00Z",
        decision_at="2023-07-01T23:59:59.999999Z",
    ) == listing
    assert (
        listing_as_of(
            [listing, listing_correction],
            security_id=listing["security_id"],
            mic="XNAS",
            as_of="2023-08-01T00:00:00Z",
            decision_at="2023-07-02T00:00:00Z",
        )
        is None
    )


def test_equal_rank_interval_corrections_fail_closed() -> None:
    initial = deepcopy(_records("us", "security_identifiers")[1])
    initial["valid_until"] = None
    first = deepcopy(initial)
    first.update(
        id="10000000-0000-4000-8000-000000000098",
        valid_until="2023-07-01T00:00:00Z",
        available_at="2023-07-02T00:00:00Z",
        recorded_at="2023-07-02T00:00:00Z",
        revision=1,
    )
    conflict = deepcopy(first)
    conflict.update(
        id="10000000-0000-4000-8000-000000000099",
        valid_until="2023-08-01T00:00:00Z",
    )
    with pytest.raises(AmbiguousHistoricalResolution):
        security_identifier_as_of(
            [initial, first, conflict],
            identifier_type="ticker",
            value="NEW",
            identifier_scope="XNAS",
            as_of="2023-07-15T00:00:00Z",
            decision_at="2023-07-02T00:00:00Z",
        )


def test_brazil_identity_keeps_bvmf_and_currency_explicit() -> None:
    security_id = "8a3d9d9d-0d9e-4a23-915b-2f94d0f62311"
    identifier = security_identifier_as_of(
        _records("brazil", "security_identifiers"),
        identifier_type="ticker",
        value="BR3",
        identifier_scope="BVMF",
        as_of="2024-01-01T00:00:00Z",
        decision_at="2021-01-02T00:00:00Z",
    )
    listing = listing_as_of(
        _records("brazil", "listings"),
        security_id=security_id,
        mic="BVMF",
        as_of="2024-01-01T00:00:00Z",
        decision_at="2021-01-02T00:00:00Z",
    )

    assert identifier is not None and identifier["security_id"] == security_id
    assert listing is not None
    assert listing["mic"] == "BVMF"
    assert listing["currency"] == "BRL"
    assert universe_as_of(
        _records("brazil", "memberships"),
        universe_id="brazil-benchmark",
        as_of="2024-01-01T00:00:00Z",
        decision_at="2021-01-03T00:00:00Z",
    ) == (security_id,)


def test_equal_rank_identity_conflict_fails_closed() -> None:
    rows = _records("us", "security_identifiers")
    conflict = deepcopy(rows[0])
    conflict["id"] = "10000000-0000-4000-8000-000000000999"
    conflict["security_id"] = "5b27f9b7-2c2a-4ab7-9691-7ad013a0a2b1"

    with pytest.raises(AmbiguousHistoricalResolution):
        security_identifier_as_of(
            [*rows, conflict],
            identifier_type="ticker",
            value="OLD",
            identifier_scope="XNAS",
            as_of="2021-06-01T00:00:00Z",
            decision_at="2021-01-02T00:00:00Z",
        )


def test_calendar_fixture_pins_holidays_early_close_and_availability() -> None:
    rows = CALENDAR_FIXTURE["sessions"]
    version = "fixture-xnas-2025-v1"
    assert trading_session_at(
        rows,
        mic="XNAS",
        calendar_version=version,
        timestamp="2025-01-02T14:30:00Z",
        decision_at="2024-11-30T23:59:59.999999Z",
    ) is None
    assert trading_session_at(
        rows,
        mic="XNAS",
        calendar_version=version,
        timestamp="2025-01-02T14:30:00Z",
        decision_at="2024-12-01T00:00:00Z",
    )["id"] == "10000000-0000-4000-8000-000000000402"
    assert trading_session_at(
        rows,
        mic="XNAS",
        calendar_version=version,
        timestamp="2025-01-01T15:00:00Z",
        decision_at="2024-12-01T00:00:00Z",
    ) is None
    assert trading_session_at(
        rows,
        mic="XNAS",
        calendar_version=version,
        timestamp="2025-01-03T17:59:59.999999Z",
        decision_at="2024-12-01T00:00:00Z",
    )["id"] == "10000000-0000-4000-8000-000000000403"
    assert trading_session_at(
        rows,
        mic="XNAS",
        calendar_version=version,
        timestamp="2025-01-03T18:00:00Z",
        decision_at="2024-12-01T00:00:00Z",
    ) is None


def test_after_close_clock_uses_next_explicit_session() -> None:
    rows = CALENDAR_FIXTURE["sessions"]
    version = "fixture-xnas-2025-v1"
    assert after_close_execution_session(
        rows,
        mic="XNAS",
        calendar_version=version,
        decision_at="2025-01-02T21:00:00Z",
    )["id"] == "10000000-0000-4000-8000-000000000403"
    assert after_close_execution_session(
        rows,
        mic="XNAS",
        calendar_version=version,
        decision_at="2025-01-03T18:00:00Z",
    )["id"] == "10000000-0000-4000-8000-000000000404"
    assert next_trading_session(
        rows,
        mic="BVMF",
        calendar_version="fixture-bvmf-2025-v1",
        after="2025-04-01T20:00:00Z",
        decision_at="2024-12-01T00:00:00Z",
    )["id"] == "10000000-0000-4000-8000-000000000407"
    with pytest.raises(HistoricalResolutionError, match="after_close_next_session"):
        after_close_execution_session(
            rows,
            mic="XNAS",
            calendar_version=version,
            decision_at="2025-01-02T20:59:59.999999Z",
        )


def test_calendar_fingerprints_match_manifests() -> None:
    for manifest in CALENDAR_FIXTURE["manifests"]:
        assert calendar_fingerprint(
            CALENDAR_FIXTURE["sessions"],
            mic=manifest["mic"],
            calendar_version=manifest["calendar_version"],
        ) == manifest["session_fingerprint"]
