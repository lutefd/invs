from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import pytest

from research.documents import publish_document
from research.events import (
    EventProposalConflictError,
    EventProposalValidationError,
    build_event_proposal,
    metadata_revision_input,
    review_event_proposal,
    write_event_proposal,
)

DECISION_AT = datetime(2026, 8, 29, 12, 0, tzinfo=UTC)


def _document(tmp_path: Path):
    return publish_document(
        b"# Capex\nSupplier capacity is tight.\n",
        source="fixture",
        source_document_id="event-doc-001",
        media_type="text/plain",
        source_uri="fixture://event-doc-001",
        available_at=DECISION_AT,
        retrieved_at=DECISION_AT,
        documents_root=tmp_path / "documents",
        document_id="bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
    )


def _proposal(tmp_path: Path) -> dict:
    document = _document(tmp_path)
    return build_event_proposal(
        document,
        event_type="capex_guidance",
        payload={"direction": "up", "period": "2027"},
        source_spans=[
            {
                "locator": "page:1;chars:8-36",
                "text_sha256": "3aa0d7aa6b648e9eaf2fc94464ac51e0330faebc961196c74d64d8702360f6ff",
                "excerpt": "Supplier capacity is tight.\n",
            }
        ],
        prompt_version="fixture-prompt-1",
        model="fixture-model",
        parameters={"temperature": 0},
        confidence=0.91,
        proposed_at=DECISION_AT,
    )


def test_event_proposal_verifies_exact_source_span(tmp_path: Path) -> None:
    proposal = _proposal(tmp_path)
    assert proposal["status"] == "proposed"
    assert proposal["review"] is None


def test_event_proposal_rejects_bad_source_hash(tmp_path: Path) -> None:
    document = _document(tmp_path)
    with pytest.raises(EventProposalValidationError, match="hash mismatch"):
        build_event_proposal(
            document,
            event_type="theme_exposure",
            payload={"theme": "ai-infrastructure"},
            source_spans=[{"locator": "chars:0-7", "text_sha256": "a" * 64}],
            prompt_version="fixture-prompt-1",
            model="fixture-model",
            confidence=0.5,
            proposed_at=DECISION_AT,
        )


def test_human_review_is_a_new_revision_and_flattens_for_metadata(tmp_path: Path) -> None:
    proposal = _proposal(tmp_path)
    reviewed = review_event_proposal(
        proposal,
        status="accepted",
        reviewer_id="researcher",
        note="Span and capex direction agree with the source.",
        reviewed_at=DECISION_AT,
    )

    assert reviewed["revision"] == 2
    assert reviewed["review"]["method"] == "human"
    metadata_input = metadata_revision_input(reviewed)
    assert metadata_input["review_method"] == "human"
    assert metadata_input["review_note"].startswith("Span")


def test_review_cannot_be_accepted_without_human_identity(tmp_path: Path) -> None:
    with pytest.raises(EventProposalValidationError, match="reviewer_id"):
        review_event_proposal(
            _proposal(tmp_path),
            status="accepted",
            reviewer_id="",
            note="accepted",
            reviewed_at=DECISION_AT,
        )


def test_event_proposal_revision_is_immutable(tmp_path: Path) -> None:
    proposal = _proposal(tmp_path)
    path = write_event_proposal(proposal, events_root=tmp_path / "events")
    assert path.read_bytes().endswith(b"\n")
    with pytest.raises(EventProposalConflictError):
        write_event_proposal({**proposal, "payload": {"direction": "down"}}, events_root=tmp_path / "events")
