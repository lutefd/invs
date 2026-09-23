from __future__ import annotations

import hashlib
import json
from datetime import date, datetime, time, timedelta, timezone
from pathlib import Path
from typing import Any

import pytest
import yaml

import research.retrospective_discovery as backfill


_DATA_SOURCE_ID = "60000000-0000-4000-8000-000000000001"
_INGESTION_RUN_ID = "70000000-0000-4000-8000-000000000001"
_MEMBERS = [
    {
        "ticker": ticker,
        "security_id": f"10000000-0000-4000-8000-{index:012d}",
        "issuer_id": f"20000000-0000-4000-8000-{index:012d}",
        "sector": "technology",
    }
    for index, ticker in enumerate(("AAA", "BBB", "CCC"), start=1)
]


def _session(day: date) -> dict[str, Any]:
    return {
        "mic": "XNAS",
        "exchange_timezone": "America/New_York",
        "session_date": day.isoformat(),
        "session_status": "open",
        "open_at": f"{day.isoformat()}T13:30:00Z",
        "close_at": f"{day.isoformat()}T20:00:00Z",
        "is_early_close": False,
        "available_at": "2026-09-23T15:00:00Z",
        "revision": 1,
    }


def _fixture(tmp_path: Path) -> tuple[Path, Path, Path, list[dict[str, Any]]]:
    profile = {
        "schema_version": "1.0.0",
        "universe_id": "30000000-0000-4000-8000-000000000001",
        "name": "test-current-universe",
        "membership_as_of": "2026-09-06",
        "members": _MEMBERS,
    }
    profile_path = tmp_path / "profile.yaml"
    profile_path.write_text(yaml.safe_dump(profile), encoding="utf-8")

    sessions = [_session(date(2026, 9, 4)), _session(date(2026, 9, 22))]
    calendar = {
        "manifest": {
            "data_source_id": "50000000-0000-4000-8000-000000000001",
            "mic": "XNAS",
            "exchange_timezone": "America/New_York",
            "calendar_version": "test-xnas-calendar",
            "session_fingerprint": backfill._calendar_session_fingerprint(sessions),
            "session_count": len(sessions),
            "available_at": "2026-09-23T15:00:00Z",
        },
        "sessions": sessions,
    }
    calendar_path = tmp_path / "calendar-snapshot.json"
    calendar_path.write_text(json.dumps(calendar, separators=(",", ":")), encoding="utf-8")

    data_root = tmp_path / "data"
    receive_time = "2026-09-23T15:00:00Z"
    ingest_time = "2026-09-23T15:05:00Z"
    prices: list[dict[str, Any]] = []
    observed_dates: list[date] = []
    cursor = date(2025, 9, 1)
    while cursor <= date(2026, 9, 22):
        if cursor.weekday() < 5:
            observed_dates.append(cursor)
        cursor += timedelta(days=1)

    for member_index, member in enumerate(_MEMBERS):
        symbol_dir = data_root / "normalized" / "prices" / "yahoo" / member["ticker"]
        symbol_dir.mkdir(parents=True)
        part_bytes = f"synthetic content for {member['ticker']}\n".encode()
        part_sha = hashlib.sha256(part_bytes).hexdigest()
        (symbol_dir / "part.parquet").write_bytes(part_bytes)
        manifest = {"parts": [{"path": "part.parquet", "sha256": part_sha}]}
        (symbol_dir / "manifest.json").write_text(
            json.dumps(manifest, sort_keys=True) + "\n", encoding="utf-8"
        )
        for row_index, observed_day in enumerate(observed_dates):
            close = 100 + member_index * 10 + row_index * (member_index + 1) / 10
            prices.append(
                {
                    "security_id": member["security_id"],
                    "source": "yahoo",
                    "interval": "1d",
                    "price_basis": "split_adjusted",
                    "currency": "USD",
                    "observed_at": f"{observed_day.isoformat()}T20:00:00Z",
                    "available_at": receive_time,
                    "ingested_at": ingest_time,
                    "open": close - 0.1,
                    "high": close + 0.5,
                    "low": close - 0.5,
                    "close": close,
                    "volume": 1_000 + row_index,
                    "raw_payload_hash": hashlib.sha256(
                        f"{member['ticker']}:{observed_day}".encode()
                    ).hexdigest(),
                    "data_source_id": _DATA_SOURCE_ID,
                    "ingestion_run_id": _INGESTION_RUN_ID,
                    "raw_record_locator": f"{member['ticker']}:{observed_day.isoformat()}",
                    "manifest_path": str(symbol_dir / "manifest.json"),
                    "part_path": str(symbol_dir / "part.parquet"),
                    "part_sha256": part_sha,
                }
            )
    return profile_path, calendar_path, data_root, prices


def _install_fake_catalog(monkeypatch: pytest.MonkeyPatch, prices: list[dict[str, Any]]) -> None:
    class FakeCatalog:
        def __init__(self, _root: Path) -> None:
            self.connection = self

        def register(self) -> FakeCatalog:
            return self

        def close(self) -> None:
            pass

    def load_prices(
        _catalog: FakeCatalog,
        *,
        security_ids: list[str],
        observed_through: datetime,
        available_through: datetime,
        **_kwargs: Any,
    ) -> list[dict[str, Any]]:
        return [
            dict(row)
            for row in prices
            if row["security_id"] in security_ids
            and datetime.fromisoformat(row["observed_at"].replace("Z", "+00:00")) <= observed_through
            and datetime.fromisoformat(row["available_at"].replace("Z", "+00:00")) <= available_through
        ]

    monkeypatch.setattr(backfill, "ResearchCatalog", FakeCatalog)
    monkeypatch.setattr(backfill, "_load_installed_prices", load_prices)


def test_retrospective_backfill_pins_hindsight_inputs_and_reuses_identity(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    profile_path, calendar_path, data_root, prices = _fixture(tmp_path)
    _install_fake_catalog(monkeypatch, prices)
    output_root = tmp_path / "published"

    manifest_path = backfill.publish_retrospective_backfill(
        profile_path=profile_path,
        calendar_snapshot_path=calendar_path,
        data_root=data_root,
        output_root=output_root,
        start_date="2026-09-04",
        end_date="2026-09-22",
        candidate_count=2,
        reconstructed_at="2026-09-23T18:00:00Z",
        git_commit="a" * 40,
    )
    document = backfill.validate_retrospective_backfill(manifest_path)

    assert document["generator_version"] == backfill.GENERATOR_VERSION
    assert document["observation_mode"] == "retrospective_current_universe"
    assert document["universe_basis"] == "current_profile_membership_snapshot"
    assert document["profile"]["membership_as_of"] == "2026-09-06"
    assert document["calendar"]["session_count"] == 2
    assert [item["market_session"] for item in document["sessions"]] == [
        "2026-09-04",
        "2026-09-22",
    ]
    for session in document["sessions"]:
        assert len(session["rankings"]) == len(_MEMBERS)
        assert len(session["price_bars"]) == len(_MEMBERS)
        assert sum(row["candidate"] for row in session["rankings"]) == 2
        for bar in session["price_bars"]:
            assert bar["available_at"] == "2026-09-23T15:00:00.000000Z"
            assert bar["ingested_at"] == "2026-09-23T15:05:00.000000Z"
            assert bar["available_at"] > session["decision_at"]
            pinned_manifest = output_root / bar["manifest_path"]
            pinned_part = output_root / bar["part_path"]
            assert hashlib.sha256(pinned_manifest.read_bytes()).hexdigest() == bar["manifest_sha256"]
            assert hashlib.sha256(pinned_part.read_bytes()).hexdigest() == bar["part_sha256"]
            assert bar["source_manifest_path"].startswith("normalized/prices/yahoo/")
            assert bar["source_part_path"].endswith("/part.parquet")

    pinned_profile = output_root / document["profile"]["snapshot_path"]
    pinned_calendar = output_root / document["calendar"]["snapshot_path"]
    assert hashlib.sha256(pinned_profile.read_bytes()).hexdigest() == document["profile"]["profile_sha256"]
    assert hashlib.sha256(pinned_calendar.read_bytes()).hexdigest() == document["calendar"]["snapshot_sha256"]

    retry_path = backfill.publish_retrospective_backfill(
        profile_path=profile_path,
        calendar_snapshot_path=calendar_path,
        data_root=data_root,
        output_root=output_root,
        start_date="2026-09-04",
        end_date="2026-09-22",
        candidate_count=2,
        reconstructed_at="2026-09-23T19:00:00Z",
        git_commit="b" * 40,
    )
    assert retry_path == manifest_path
    assert json.loads(retry_path.read_text(encoding="utf-8")) == document


def test_retrospective_backfill_rejects_truncated_calendar_export(tmp_path: Path) -> None:
    _, calendar_path, _, _ = _fixture(tmp_path)
    snapshot = json.loads(calendar_path.read_text(encoding="utf-8"))
    snapshot["sessions"].pop()
    calendar_path.write_text(json.dumps(snapshot), encoding="utf-8")

    with pytest.raises(backfill.RetrospectiveBackfillError, match="manifest declares 2"):
        backfill._load_calendar_snapshot(calendar_path)


def test_retrospective_backfill_requires_calendar_window_boundaries(tmp_path: Path) -> None:
    sessions = [_session(date(2026, 9, 4)), _session(date(2026, 9, 22))]
    selected = []
    for item in sessions:
        normalized = dict(item)
        normalized["_session_date"] = date.fromisoformat(item["session_date"])
        normalized["_close_at"] = datetime.fromisoformat(item["close_at"].replace("Z", "+00:00"))
        selected.append(normalized)

    with pytest.raises(backfill.RetrospectiveBackfillError, match="does not cover both"):
        backfill._session_rows(
            selected,
            start=date(2026, 9, 3),
            end=date(2026, 9, 22),
            cutoff=datetime(2026, 9, 23, 18, tzinfo=timezone.utc),
        )
