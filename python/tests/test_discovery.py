from __future__ import annotations

import json
from pathlib import Path

import yaml

from research.batches import ValidatedFeatureBatch
from research.discovery import publish_discovery_index, read_discovery_index


def _member(index: int, ticker: str) -> dict[str, str]:
    return {
        "ticker": ticker,
        "security_id": f"10000000-0000-4000-8000-{index:012d}",
        "issuer_id": f"20000000-0000-4000-8000-{index:012d}",
        "sector": "technology",
    }


def _observation(member: dict[str, str], value: str) -> dict[str, object]:
    return {
        "security_id": member["security_id"],
        "decision_at": "2026-09-08T22:00:00Z",
        "features": {
            "return_1m": value,
            "return_3m": value,
            "return_6m": value,
            "return_12m": value,
            "realized_volatility_1m": str(1 / int(value)),
            "max_drawdown_1m": str(-1 / int(value)),
        },
    }


def test_discovery_ranks_and_reuses_one_index_per_market_session(monkeypatch, tmp_path: Path) -> None:
    members = [_member(1, "AAA"), _member(2, "BBB"), _member(3, "CCC")]
    profile = {
        "schema_version": "1.0.0",
        "universe_id": "30000000-0000-4000-8000-000000000001",
        "name": "test-universe",
        "membership_as_of": "2026-09-08",
        "members": members,
    }
    profile_path = tmp_path / "profile.yaml"
    profile_path.write_text(yaml.safe_dump(profile), encoding="utf-8")
    features_root = tmp_path / "features"
    batch_path = features_root / "batches" / "momentum" / "manifest.json"
    batch_path.parent.mkdir(parents=True)
    batch_path.write_text("{}\n", encoding="utf-8")
    batch = ValidatedFeatureBatch(
        batch_path,
        {
            "feature_set": "market-momentum",
            "feature_set_version": "1.0.0",
            "decision_schedule": ["2026-09-08T22:00:00Z"],
            "universe": {
                "security_ids": [item["security_id"] for item in members],
                "fingerprint": "a" * 64,
            },
            "batch": {"batch_id": "40000000-0000-4000-8000-000000000001"},
            "input_fingerprint": "b" * 64,
            "input_fitness": [
                {
                    "dataset": "prices",
                    "historical_fitness": "installation_replay_only",
                    "availability_policy": "conservative_receipt_time",
                }
            ],
            "rejected": [],
        },
        tuple(_observation(member, str(index)) for index, member in enumerate(members, start=1)),
    )
    monkeypatch.setattr("research.discovery._validated_batch", lambda *args, **kwargs: batch)

    manifest = publish_discovery_index(
        profile_path=profile_path,
        momentum_batch_manifest=batch_path,
        features_root=features_root,
        registry_path=tmp_path / "unused.json",
        output_root=tmp_path / "discovery",
        market_session="2026-09-08",
        candidate_count=2,
    )
    document = read_discovery_index(manifest)
    assert [row["ticker"] for row in document["rows"]] == ["CCC", "BBB", "AAA"]
    assert [row["candidate"] for row in document["rows"]] == [True, True, False]

    reused = publish_discovery_index(
        profile_path=profile_path,
        momentum_batch_manifest=batch_path,
        features_root=features_root,
        registry_path=tmp_path / "unused.json",
        output_root=tmp_path / "discovery",
        market_session="2026-09-08",
        candidate_count=1,
    )
    assert reused == manifest
    assert json.loads(reused.read_text(encoding="utf-8"))["policy"]["candidate_count"] == 2
