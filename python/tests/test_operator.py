from __future__ import annotations

import json
from pathlib import Path

import yaml

from research.operator import materialize_operator_snapshot

SECURITY_A = "10000000-0000-4000-8000-000000000001"
SECURITY_B = "10000000-0000-4000-8000-000000000002"


class _Catalog:
    def register(self) -> _Catalog:
        return self


def _inputs(tmp_path: Path) -> tuple[Path, Path, list[dict[str, object]]]:
    profile = {
        "schema_version": "1.0.0",
        "universe_id": "20000000-0000-4000-8000-000000000001",
        "membership_as_of": "2026-09-01",
        "members": [
            {
                "ticker": "AAA",
                "security_id": SECURITY_A,
                "issuer_id": "50000000-0000-4000-8000-000000000001",
                "sector": "technology",
                "themes": ["ai"],
            },
            {
                "ticker": "BBB",
                "security_id": SECURITY_B,
                "issuer_id": "50000000-0000-4000-8000-000000000002",
                "sector": "financials",
                "themes": ["banking"],
            },
        ],
    }
    profile_path = tmp_path / "profile.yaml"
    profile_path.write_text(yaml.safe_dump(profile), encoding="utf-8")
    calendar = {
        "manifest": {
            "data_source_id": "60000000-0000-4000-8000-000000000001",
            "mic": "XNAS",
            "calendar_version": "xnas_2026_test",
            "session_fingerprint": "a" * 64,
            "available_at": "2026-09-01T00:00:00+00:00",
        },
        "sessions": [
            {
                "session_date": day,
                "session_status": "open",
                "open_at": f"{day}T13:30:00+00:00",
                "close_at": f"{day}T20:00:00+00:00",
                "is_early_close": False,
                "available_at": "2026-09-01T00:00:00+00:00",
            }
            for day in ("2026-09-04", "2026-09-08")
        ],
    }
    calendar_path = tmp_path / "calendar.json"
    calendar_path.write_text(json.dumps(calendar), encoding="utf-8")
    prices = [
        {
            "security_id": security_id,
            "session_date": "2026-09-04",
            "observed_at": "2026-09-04T20:00:00Z",
            "available_at": "2026-09-04T22:00:00Z",
            "currency": "USD",
            "price_basis": "split_adjusted",
            "open": "100",
            "high": "102",
            "low": "99",
            "close": "101",
            "volume": "1000000",
            "has_volume": True,
            "source_record_id": f"test/{security_id}",
        }
        for security_id in (SECURITY_A, SECURITY_B)
    ]
    return profile_path, calendar_path, prices


def test_materializer_gates_pre_activation_sessions(monkeypatch, tmp_path: Path) -> None:
    profile, calendar, prices = _inputs(tmp_path)
    monkeypatch.setattr("research.operator.ResearchCatalog", lambda _: _Catalog())
    monkeypatch.setattr("research.operator._price_rows", lambda *_args, **_kwargs: prices)

    result = materialize_operator_snapshot(
        profile_path=profile,
        calendar_snapshot_path=calendar,
        data_root=tmp_path,
        output_root=tmp_path / "operator",
        decision_at="2026-09-04T22:30:00Z",
        not_before="2026-09-05T00:00:00Z",
    )

    assert result["status"] == "awaiting_forward_session"
    assert result["paper"] is None
    assert Path(result["feature"]["universe"]).is_file()


def test_materializer_builds_valid_session_input_bundle(monkeypatch, tmp_path: Path) -> None:
    from research.paper import preflight_paper_session

    profile, calendar, prices = _inputs(tmp_path)
    monkeypatch.setattr("research.operator.ResearchCatalog", lambda _: _Catalog())
    monkeypatch.setattr("research.operator._price_rows", lambda *_args, **_kwargs: prices)

    result = materialize_operator_snapshot(
        profile_path=profile,
        calendar_snapshot_path=calendar,
        data_root=tmp_path,
        output_root=tmp_path / "operator",
        decision_at="2026-09-04T22:30:00Z",
        not_before="2026-09-01T00:00:00Z",
    )
    paper = result["paper"]
    assert paper is not None
    account = json.loads(Path(paper["spec"]).read_text(encoding="utf-8"))
    bundle = json.loads(Path(paper["inputs"]).read_text(encoding="utf-8"))

    preflight = preflight_paper_session(
        account,
        data_root=tmp_path,
        session_date="2026-09-04",
        decision_at="2026-09-04T22:30:00Z",
        input_references=bundle["inputs"],
    )

    assert result["status"] == "paper_ready"
    assert preflight["active_security_ids"] == [SECURITY_A, SECURITY_B]
