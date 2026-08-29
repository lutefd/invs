"""Reviewed, source-span-grounded event proposal artifacts."""

from __future__ import annotations

import re
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, Final
from uuid import UUID, uuid5

from .documents import (
    DocumentArtifactConflictError,
    DocumentArtifactValidationError,
    ValidatedDocument,
    _canonical_json,
    _hash,
    _nonempty,
    _sha256_bytes,
    _strict_json,
    _timestamp,
    _uuid,
    _write_immutable,
    read_document,
    read_document_text,
)

SCHEMA_VERSION: Final[str] = "1.0.0"
EXTRACTION_VERSION: Final[str] = "event-proposal-1.0.0"
EVENT_TYPES: Final[frozenset[str]] = frozenset(
    {
        "capex_guidance",
        "production_guidance",
        "material_customer_supplier",
        "financing",
        "theme_exposure",
    }
)

_PROPOSAL_NAMESPACE = UUID("c0e4a54f-5886-5cde-a5e6-92a2e79f6829")
_SPAN_LOCATOR = re.compile(r"^(?:page:(?P<page>[1-9][0-9]*);)?chars:(?P<start>[0-9]+)-(?P<end>[0-9]+)$")
_PROPOSAL_FIELDS = frozenset(
    {
        "schema_version",
        "proposal_id",
        "revision",
        "document_id",
        "event_type",
        "payload",
        "source_spans",
        "prompt_version",
        "model",
        "parameters",
        "extraction_version",
        "source_document_sha256",
        "confidence",
        "status",
        "proposed_at",
        "review",
    }
)
_SPAN_FIELDS = frozenset({"locator", "text_sha256", "excerpt"})
_REVIEW_FIELDS = frozenset({"reviewer_id", "method", "reviewed_at", "note"})


class EventProposalError(ValueError):
    """Base error for invalid event proposal artifacts."""


class EventProposalConflictError(DocumentArtifactConflictError, EventProposalError):
    """Raised when an immutable proposal path contains different bytes."""


class EventProposalValidationError(DocumentArtifactValidationError, EventProposalError):
    """Raised when a proposal or source span cannot be verified."""


def _event_nonempty(value: Any, *, field: str) -> str:
    try:
        return _nonempty(value, field=field)
    except DocumentArtifactValidationError as error:
        raise EventProposalValidationError(str(error)) from error


def _event_hash(value: Any, *, field: str) -> str | None:
    try:
        return _hash(value, field=field)
    except DocumentArtifactValidationError as error:
        raise EventProposalValidationError(str(error)) from error


def _event_uuid(value: Any, *, field: str) -> str | None:
    try:
        return _uuid(value, field=field)
    except DocumentArtifactValidationError as error:
        raise EventProposalValidationError(str(error)) from error


def _event_timestamp(value: datetime | str, *, field: str) -> tuple[str, datetime]:
    try:
        return _timestamp(value, field=field)
    except DocumentArtifactValidationError as error:
        raise EventProposalValidationError(str(error)) from error


def _document(value: ValidatedDocument | str | Path) -> ValidatedDocument:
    if isinstance(value, ValidatedDocument):
        return value
    return read_document(value)


def _span_bounds(locator: str, *, text_length: int) -> tuple[int, int]:
    match = _SPAN_LOCATOR.fullmatch(locator)
    if match is None:
        raise EventProposalValidationError(
            f"source span locator must be chars:start-end or page:N;chars:start-end: {locator!r}"
        )
    start, end = int(match.group("start")), int(match.group("end"))
    if start >= end or end > text_length:
        raise EventProposalValidationError(f"source span {locator!r} exceeds extracted text")
    return start, end


def verify_source_spans(
    proposal: dict[str, Any], document: ValidatedDocument | str | Path
) -> tuple[dict[str, Any], ...]:
    """Verify proposal source hashes and excerpts against the text artifact."""

    validated = _document(document)
    manifest = validated.manifest
    expected_document_id = _event_uuid(proposal.get("document_id"), field="document_id")
    if expected_document_id != manifest["document_id"]:
        raise EventProposalValidationError("proposal document_id does not match document manifest")
    expected_raw_hash = manifest["raw_artifact"]["sha256"]
    if proposal.get("source_document_sha256") != expected_raw_hash:
        raise EventProposalValidationError("proposal source_document_sha256 does not match raw document")
    text = read_document_text(validated)
    spans = proposal.get("source_spans")
    if not isinstance(spans, list) or not spans:
        raise EventProposalValidationError("source_spans must be a non-empty array")
    verified: list[dict[str, Any]] = []
    for index, value in enumerate(spans):
        if not isinstance(value, dict) or frozenset(value) - _SPAN_FIELDS or "locator" not in value or "text_sha256" not in value:
            raise EventProposalValidationError(f"source_spans[{index}] has invalid fields")
        locator = _event_nonempty(value["locator"], field=f"source_spans[{index}].locator")
        text_hash = _event_hash(value["text_sha256"], field=f"source_spans[{index}].text_sha256")
        assert text_hash is not None
        start, end = _span_bounds(locator, text_length=len(text))
        actual_text = text[start:end]
        actual_hash = _sha256_bytes(actual_text.encode("utf-8"))
        if actual_hash != text_hash:
            raise EventProposalValidationError(
                f"source_spans[{index}] hash mismatch: expected {text_hash}, got {actual_hash}"
            )
        if "excerpt" in value and value["excerpt"] != actual_text:
            raise EventProposalValidationError(f"source_spans[{index}].excerpt does not match text")
        verified.append(dict(value))
    return tuple(verified)


def _validate_proposal(proposal: dict[str, Any]) -> dict[str, Any]:
    if frozenset(proposal) != _PROPOSAL_FIELDS:
        raise EventProposalValidationError(
            f"proposal fields mismatch: missing={sorted(_PROPOSAL_FIELDS - frozenset(proposal))}, "
            f"extra={sorted(frozenset(proposal) - _PROPOSAL_FIELDS)}"
        )
    if proposal["schema_version"] != SCHEMA_VERSION:
        raise EventProposalValidationError("unsupported proposal schema_version")
    _event_uuid(proposal["proposal_id"], field="proposal_id")
    if not isinstance(proposal["revision"], int) or proposal["revision"] < 1:
        raise EventProposalValidationError("revision must be positive")
    _event_uuid(proposal["document_id"], field="document_id")
    if proposal["event_type"] not in EVENT_TYPES:
        raise EventProposalValidationError("unsupported event_type")
    if not isinstance(proposal["payload"], dict) or not proposal["payload"]:
        raise EventProposalValidationError("payload must be a non-empty object")
    if not isinstance(proposal["parameters"], dict):
        raise EventProposalValidationError("parameters must be an object")
    for field in ("prompt_version", "model", "extraction_version"):
        _event_nonempty(proposal[field], field=field)
    _event_hash(proposal["source_document_sha256"], field="source_document_sha256")
    if not isinstance(proposal["confidence"], (int, float)) or not 0 <= proposal["confidence"] <= 1:
        raise EventProposalValidationError("confidence must be between 0 and 1")
    if proposal["status"] not in {"proposed", "accepted", "rejected", "superseded"}:
        raise EventProposalValidationError("unsupported proposal status")
    _event_timestamp(proposal["proposed_at"], field="proposed_at")
    review = proposal["review"]
    if proposal["status"] == "proposed":
        if review is not None:
            raise EventProposalValidationError("proposed event cannot carry review metadata")
    else:
        if not isinstance(review, dict) or frozenset(review) != _REVIEW_FIELDS:
            raise EventProposalValidationError("reviewed event requires complete human review metadata")
        _event_nonempty(review["reviewer_id"], field="review.reviewer_id")
        if review["method"] != "human":
            raise EventProposalValidationError("review.method must be human")
        _event_timestamp(review["reviewed_at"], field="review.reviewed_at")
        _event_nonempty(review["note"], field="review.note")
    return proposal


def build_event_proposal(
    document: ValidatedDocument | str | Path,
    *,
    event_type: str,
    payload: dict[str, Any],
    source_spans: list[dict[str, Any]],
    prompt_version: str,
    model: str,
    parameters: dict[str, Any] | None = None,
    extraction_version: str = EXTRACTION_VERSION,
    confidence: float,
    proposal_id: str | None = None,
    proposed_at: datetime | str | None = None,
) -> dict[str, Any]:
    """Build a proposed event only after verifying its document source spans."""

    validated = _document(document)
    if validated.text_path is None:
        raise EventProposalValidationError("event proposals require an extracted text artifact")
    manifest = validated.manifest
    document_id = manifest["document_id"]
    if proposal_id is None:
        identity = {"document_id": document_id, "event_type": event_type, "payload": payload, "source_spans": source_spans}
        proposal_id = str(uuid5(_PROPOSAL_NAMESPACE, _canonical_json(identity).decode("utf-8")))
    else:
        proposal_id = _event_uuid(proposal_id, field="proposal_id")
        assert proposal_id is not None
    proposed_text = (datetime.now(UTC) if proposed_at is None else proposed_at)
    proposed_text, _ = _event_timestamp(proposed_text, field="proposed_at")
    proposal = {
        "schema_version": SCHEMA_VERSION,
        "proposal_id": proposal_id,
        "revision": 1,
        "document_id": document_id,
        "event_type": event_type,
        "payload": payload,
        "source_spans": source_spans,
        "prompt_version": prompt_version,
        "model": model,
        "parameters": {} if parameters is None else parameters,
        "extraction_version": extraction_version,
        "source_document_sha256": manifest["raw_artifact"]["sha256"],
        "confidence": confidence,
        "status": "proposed",
        "proposed_at": proposed_text,
        "review": None,
    }
    _validate_proposal(proposal)
    verify_source_spans(proposal, validated)
    return proposal


def review_event_proposal(
    proposal: dict[str, Any] | str | Path,
    *,
    status: str,
    reviewer_id: str,
    note: str,
    reviewed_at: datetime | str,
) -> dict[str, Any]:
    """Return an append-only human review revision for a proposal."""

    value = read_event_proposal(proposal) if isinstance(proposal, (str, Path)) else dict(proposal)
    _validate_proposal(value)
    if status not in {"accepted", "rejected", "superseded"}:
        raise EventProposalValidationError("review status must be accepted, rejected, or superseded")
    reviewed_text, _ = _event_timestamp(reviewed_at, field="reviewed_at")
    reviewed = dict(value)
    reviewed["revision"] = value["revision"] + 1
    reviewed["status"] = status
    reviewed["review"] = {
        "reviewer_id": _event_nonempty(reviewer_id, field="reviewer_id"),
        "method": "human",
        "reviewed_at": reviewed_text,
        "note": _event_nonempty(note, field="note"),
    }
    _validate_proposal(reviewed)
    return reviewed


def write_event_proposal(proposal: dict[str, Any], *, events_root: str | Path) -> Path:
    """Publish one immutable JSON proposal revision."""

    _validate_proposal(proposal)
    root = Path(events_root).expanduser().resolve()
    path = root / f"proposal-{proposal['proposal_id']}" / f"revision-{proposal['revision']}.json"
    try:
        _write_immutable(path, _canonical_json(proposal) + b"\n", label="event proposal")
    except DocumentArtifactConflictError as error:
        raise EventProposalConflictError(str(error)) from error
    return path


def read_event_proposal(path: str | Path) -> dict[str, Any]:
    proposal = _strict_json(Path(path).expanduser().resolve(), label="event proposal")
    return _validate_proposal(proposal)


def metadata_revision_input(proposal: dict[str, Any]) -> dict[str, Any]:
    """Flatten artifact review metadata for the Go repository CLI."""

    _validate_proposal(proposal)
    review = proposal["review"] or {}
    return {
        "proposal_id": proposal["proposal_id"],
        "revision": proposal["revision"],
        "document_id": proposal["document_id"],
        "event_type": proposal["event_type"],
        "payload": proposal["payload"],
        "source_spans": proposal["source_spans"],
        "prompt_version": proposal["prompt_version"],
        "model": proposal["model"],
        "parameters": proposal["parameters"],
        "extraction_version": proposal["extraction_version"],
        "source_document_sha256": proposal["source_document_sha256"],
        "confidence": proposal["confidence"],
        "status": proposal["status"],
        "proposed_at": proposal["proposed_at"],
        "reviewer_id": review.get("reviewer_id", ""),
        "review_method": review.get("method", ""),
        "reviewed_at": review.get("reviewed_at"),
        "review_note": review.get("note", ""),
    }


__all__ = [
    "EVENT_TYPES",
    "EXTRACTION_VERSION",
    "SCHEMA_VERSION",
    "EventProposalConflictError",
    "EventProposalError",
    "EventProposalValidationError",
    "build_event_proposal",
    "metadata_revision_input",
    "read_event_proposal",
    "review_event_proposal",
    "verify_source_spans",
    "write_event_proposal",
]
