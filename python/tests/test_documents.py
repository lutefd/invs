from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import pytest

from research.documents import (
    DocumentArtifactConflictError,
    DocumentArtifactValidationError,
    publish_document,
    read_document,
    read_document_text,
)

DECISION_AT = datetime(2026, 8, 29, 12, 0, tzinfo=UTC)


def _publish(root: Path, *, raw: bytes = b"# Capex\nSupplier capacity is tight.\n", media_type: str = "text/plain"):
    return publish_document(
        raw,
        source="fixture",
        source_document_id="ai-infra-001",
        media_type=media_type,
        source_uri="fixture://ai-infra-001",
        published_at=DECISION_AT,
        available_at=DECISION_AT,
        retrieved_at=DECISION_AT,
        documents_root=root,
        document_id="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    )


def test_publish_document_retains_raw_and_deterministic_text(tmp_path: Path) -> None:
    published = _publish(tmp_path)

    assert published.raw_path.read_bytes() == b"# Capex\nSupplier capacity is tight.\n"
    assert read_document_text(published) == "# Capex\nSupplier capacity is tight.\n"
    assert published.manifest["text_artifact"]["status"] == "extracted"
    assert published.manifest["text_artifact"]["locators"] == [
        {"ordinal": 0, "page": 1, "section": "Capex", "start": 0, "end": 36}
    ]
    assert read_document(published.manifest_path).manifest == published.manifest


def test_publish_document_is_idempotent_for_identical_bytes(tmp_path: Path) -> None:
    first = _publish(tmp_path)
    second = _publish(tmp_path)

    assert second.manifest_path == first.manifest_path
    assert second.manifest == first.manifest


def test_publish_document_rejects_changed_bytes_at_same_identity(tmp_path: Path) -> None:
    _publish(tmp_path)
    with pytest.raises(DocumentArtifactConflictError):
        _publish(tmp_path, raw=b"different\n")


def test_failed_extraction_keeps_raw_artifact(tmp_path: Path) -> None:
    published = _publish(tmp_path, raw=b"not a pdf", media_type="application/pdf")

    text = published.manifest["text_artifact"]
    assert text["status"] == "failed"
    assert text["path"] is None
    assert text["output_sha256"] is None
    assert text["errors"]
    assert published.raw_path.exists()
    with pytest.raises(DocumentArtifactValidationError, match="no extracted text"):
        read_document_text(published)


def test_tampering_raw_bytes_fails_closed(tmp_path: Path) -> None:
    published = _publish(tmp_path)
    published.raw_path.write_bytes(b"tampered")

    with pytest.raises(DocumentArtifactValidationError, match="raw artifact hash/size mismatch"):
        read_document(published.manifest_path)


def test_html_extraction_hides_script_and_preserves_heading_locator(tmp_path: Path) -> None:
    published = _publish(
        tmp_path,
        raw=b"<h1>Networking</h1><script>ignore()</script><p>Optical demand.</p>",
        media_type="text/html",
    )

    assert read_document_text(published) == "Networking\n\nOptical demand.\n"
    assert published.manifest["text_artifact"]["locators"][0]["section"] == "Networking"
