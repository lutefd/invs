from __future__ import annotations

from datetime import UTC, datetime, timedelta
from pathlib import Path

import pytest

from research.evidence import (
    EvidencePackConflictError,
    EvidencePackValidationError,
    build_evidence_pack,
    export_research_memo,
    import_research_memo,
    read_evidence_pack,
)

DECISION_AT = datetime(2026, 8, 29, 12, 0, tzinfo=UTC)


def _ref(kind: str, identifier: str, available_at: datetime = DECISION_AT) -> dict[str, str]:
    return {
        "kind": kind,
        "id": identifier,
        "sha256": "a" * 64,
        "available_at": available_at.isoformat().replace("+00:00", "Z"),
        "locator": f"fixture:{identifier}",
    }


def _pack(tmp_path: Path) -> Path:
    return build_evidence_pack(
        decision_at=DECISION_AT,
        theme_refs=[_ref("theme", "ai-infrastructure")],
        canonical_observation_refs=[_ref("macro", "fred:DGS10")],
        feature_artifact_refs=[_ref("feature", "feature-1", DECISION_AT - timedelta(hours=1))],
        document_refs=[_ref("document", "document-1")],
        event_proposal_refs=[_ref("event", "proposal-1")],
        packs_root=tmp_path / "packs",
        created_at=DECISION_AT,
    )


def test_evidence_pack_filters_future_inputs(tmp_path: Path) -> None:
    with pytest.raises(EvidencePackValidationError, match="after decision_at"):
        build_evidence_pack(
            decision_at=DECISION_AT,
            theme_refs=[_ref("theme", "ai-infrastructure")],
            document_refs=[_ref("document", "future", DECISION_AT + timedelta(seconds=1))],
            packs_root=tmp_path / "packs",
        )


def test_evidence_pack_is_deterministic_and_verifiable(tmp_path: Path) -> None:
    first = _pack(tmp_path)
    second = _pack(tmp_path)

    assert first == second
    assert read_evidence_pack(first)["eligibility_policy"] == "available_at_le_decision_at"


def test_evidence_pack_conflict_does_not_overwrite_identity(tmp_path: Path) -> None:
    first = _pack(tmp_path)
    pack = read_evidence_pack(first)
    with pytest.raises(EvidencePackConflictError):
        build_evidence_pack(
            decision_at=DECISION_AT,
            theme_refs=[_ref("theme", "different")],
            packs_root=tmp_path / "packs",
            pack_id=pack["pack_id"],
            created_at=DECISION_AT,
        )


def test_memo_export_and_import_round_trip_references_pack(tmp_path: Path) -> None:
    pack_path = _pack(tmp_path)
    json_path, markdown_path = export_research_memo(
        pack_path,
        hypothesis_id="cccccccc-cccc-4ccc-8ccc-cccccccccccc",
        hypothesis_revision=1,
        decision_at=DECISION_AT,
        prediction_ids=["dddddddd-dddd-4ddd-8ddd-dddddddddddd"],
        referenced_ids=["ai-infrastructure", "proposal-1"],
        body="Capacity bottlenecks should benefit the reviewed supplier basket.",
        memos_root=tmp_path / "memos",
    )

    imported = import_research_memo(json_path, evidence_pack=pack_path)
    assert imported["evidence_pack_id"] == read_evidence_pack(pack_path)["pack_id"]
    assert markdown_path.read_text(encoding="utf-8").startswith("# Research memo")


def test_memo_import_rejects_tampered_markdown(tmp_path: Path) -> None:
    pack_path = _pack(tmp_path)
    json_path, markdown_path = export_research_memo(
        pack_path,
        hypothesis_id="cccccccc-cccc-4ccc-8ccc-cccccccccccc",
        hypothesis_revision=1,
        decision_at=DECISION_AT,
        prediction_ids=[],
        referenced_ids=["ai-infrastructure"],
        body="A bounded thesis.",
        memos_root=tmp_path / "memos",
    )
    markdown_path.write_text("tampered\n", encoding="utf-8")

    with pytest.raises(EvidencePackValidationError, match="Markdown hash mismatch"):
        import_research_memo(json_path)
