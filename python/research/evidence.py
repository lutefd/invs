"""Point-in-time evidence packs and self-contained research memo exports."""

from __future__ import annotations

from datetime import datetime
from pathlib import Path
from typing import Any, Final
from uuid import UUID, uuid5

from .documents import (
    DocumentArtifactConflictError,
    DocumentArtifactValidationError,
    _canonical_json,
    _hash,
    _nonempty,
    _safe_relative,
    _sha256_bytes,
    _strict_json,
    _timestamp,
    _uuid,
    _write_immutable,
)

SCHEMA_VERSION: Final[str] = "1.0.0"
ELIGIBILITY_POLICY: Final[str] = "available_at_le_decision_at"
_PACK_NAMESPACE = UUID("f0383045-d14f-50c2-a75c-1f58d8467983")
_MEMO_NAMESPACE = UUID("5b22b9ce-74af-531a-b95f-4d9d41d85769")
_PACK_FIELDS = frozenset(
    {
        "schema_version",
        "pack_id",
        "decision_at",
        "created_at",
        "eligibility_policy",
        "theme_refs",
        "canonical_observation_refs",
        "feature_artifact_refs",
        "document_refs",
        "event_proposal_refs",
        "content_sha256",
    }
)
_MEMO_FIELDS = frozenset(
    {
        "schema_version",
        "memo_id",
        "evidence_pack_id",
        "evidence_pack_sha256",
        "hypothesis_id",
        "hypothesis_revision",
        "decision_at",
        "prediction_ids",
        "referenced_ids",
        "body",
        "markdown_sha256",
    }
)
_REF_FIELDS = frozenset({"kind", "id", "sha256", "available_at", "locator", "path"})


class EvidencePackError(ValueError):
    """Base error for invalid point-in-time evidence packs or memos."""


class EvidencePackConflictError(DocumentArtifactConflictError, EvidencePackError):
    """Raised when an immutable pack or memo path contains different bytes."""


class EvidencePackValidationError(DocumentArtifactValidationError, EvidencePackError):
    """Raised when an evidence pack or memo fails closed validation."""


def _evidence_timestamp(value: datetime | str, *, field: str) -> tuple[str, datetime]:
    try:
        return _timestamp(value, field=field)
    except DocumentArtifactValidationError as error:
        raise EvidencePackValidationError(str(error)) from error


def _evidence_nonempty(value: Any, *, field: str) -> str:
    try:
        return _nonempty(value, field=field)
    except DocumentArtifactValidationError as error:
        raise EvidencePackValidationError(str(error)) from error


def _evidence_uuid(value: Any, *, field: str, nullable: bool = False) -> str | None:
    try:
        return _uuid(value, field=field, nullable=nullable)
    except DocumentArtifactValidationError as error:
        raise EvidencePackValidationError(str(error)) from error


def _evidence_hash(value: Any, *, field: str) -> str:
    try:
        result = _hash(value, field=field)
    except DocumentArtifactValidationError as error:
        raise EvidencePackValidationError(str(error)) from error
    assert result is not None
    return result


def _normalise_ref(value: Any, *, decision_at: datetime, label: str) -> dict[str, Any]:
    if not isinstance(value, dict) or not {"kind", "id", "sha256", "available_at"} <= frozenset(value):
        raise EvidencePackValidationError(f"{label} requires kind, id, sha256, and available_at")
    if frozenset(value) - _REF_FIELDS:
        raise EvidencePackValidationError(f"{label} has unknown fields")
    result = {
        "kind": _evidence_nonempty(value["kind"], field=f"{label}.kind"),
        "id": _evidence_nonempty(value["id"], field=f"{label}.id"),
        "sha256": _evidence_hash(value["sha256"], field=f"{label}.sha256"),
        "available_at": _evidence_timestamp(value["available_at"], field=f"{label}.available_at")[0],
    }
    _, available_at = _evidence_timestamp(result["available_at"], field=f"{label}.available_at")
    if available_at > decision_at:
        raise EvidencePackValidationError(
            f"{label}.available_at {result['available_at']} is after decision_at"
        )
    for field in ("locator", "path"):
        if field in value:
            result[field] = _evidence_nonempty(value[field], field=f"{label}.{field}")
            if field == "path":
                try:
                    _safe_relative(result[field], field=f"{label}.path")
                except DocumentArtifactValidationError as error:
                    raise EvidencePackValidationError(str(error)) from error
    return result


def _normalise_refs(values: list[dict[str, Any]] | tuple[dict[str, Any], ...], *, decision_at: datetime, label: str) -> list[dict[str, Any]]:
    if not isinstance(values, (list, tuple)):
        raise EvidencePackValidationError(f"{label} must be an array")
    result = [_normalise_ref(value, decision_at=decision_at, label=f"{label}[{index}]") for index, value in enumerate(values)]
    result.sort(key=lambda item: (item["kind"], item["id"], item["sha256"], item["available_at"], item.get("locator", ""), item.get("path", "")))
    identity = [(item["kind"], item["id"], item["sha256"], item.get("locator", ""), item.get("path", "")) for item in result]
    if len(set(identity)) != len(identity):
        raise EvidencePackValidationError(f"{label} contains duplicate references")
    return result


def _pack_content(pack: dict[str, Any]) -> bytes:
    content = dict(pack)
    content.pop("content_sha256", None)
    return _canonical_json(content)


def _validate_pack(pack: dict[str, Any]) -> dict[str, Any]:
    if frozenset(pack) != _PACK_FIELDS:
        raise EvidencePackValidationError(
            f"evidence pack fields mismatch: missing={sorted(_PACK_FIELDS - frozenset(pack))}, "
            f"extra={sorted(frozenset(pack) - _PACK_FIELDS)}"
        )
    if pack["schema_version"] != SCHEMA_VERSION or pack["eligibility_policy"] != ELIGIBILITY_POLICY:
        raise EvidencePackValidationError("unsupported evidence pack contract")
    _evidence_uuid(pack["pack_id"], field="pack_id")
    _, decision_at = _evidence_timestamp(pack["decision_at"], field="decision_at")
    _evidence_timestamp(pack["created_at"], field="created_at")
    for field in ("theme_refs", "canonical_observation_refs", "feature_artifact_refs", "document_refs", "event_proposal_refs"):
        _normalise_refs(pack[field], decision_at=decision_at, label=field)
    expected_hash = _sha256_bytes(_pack_content(pack))
    if pack["content_sha256"] != expected_hash:
        raise EvidencePackValidationError(
            f"content_sha256 mismatch: expected {expected_hash}, got {pack['content_sha256']}"
        )
    _evidence_hash(pack["content_sha256"], field="content_sha256")
    return pack


def build_evidence_pack(
    *,
    decision_at: datetime | str,
    theme_refs: list[dict[str, Any]] | tuple[dict[str, Any], ...] = (),
    canonical_observation_refs: list[dict[str, Any]] | tuple[dict[str, Any], ...] = (),
    feature_artifact_refs: list[dict[str, Any]] | tuple[dict[str, Any], ...] = (),
    document_refs: list[dict[str, Any]] | tuple[dict[str, Any], ...] = (),
    event_proposal_refs: list[dict[str, Any]] | tuple[dict[str, Any], ...] = (),
    packs_root: str | Path = "data/research/evidence-packs",
    pack_id: str | None = None,
    created_at: datetime | str | None = None,
) -> Path:
    """Build and immutably publish one point-in-time evidence pack."""

    decision_text, decision = _evidence_timestamp(decision_at, field="decision_at")
    created_text, _ = _evidence_timestamp(created_at or decision, field="created_at")
    core = {
        "schema_version": SCHEMA_VERSION,
        "pack_id": "",
        "decision_at": decision_text,
        "created_at": created_text,
        "eligibility_policy": ELIGIBILITY_POLICY,
        "theme_refs": _normalise_refs(theme_refs, decision_at=decision, label="theme_refs"),
        "canonical_observation_refs": _normalise_refs(canonical_observation_refs, decision_at=decision, label="canonical_observation_refs"),
        "feature_artifact_refs": _normalise_refs(feature_artifact_refs, decision_at=decision, label="feature_artifact_refs"),
        "document_refs": _normalise_refs(document_refs, decision_at=decision, label="document_refs"),
        "event_proposal_refs": _normalise_refs(event_proposal_refs, decision_at=decision, label="event_proposal_refs"),
    }
    if pack_id is None:
        pack_id = str(uuid5(_PACK_NAMESPACE, _canonical_json(core).decode("utf-8")))
    else:
        pack_id = _evidence_uuid(pack_id, field="pack_id")
        assert pack_id is not None
    core["pack_id"] = pack_id
    content_hash = _sha256_bytes(_pack_content({**core, "content_sha256": ""}))
    pack = {**core, "content_sha256": content_hash}
    _validate_pack(pack)
    root = Path(packs_root).expanduser().resolve()
    path = root / f"pack-{pack_id}.json"
    try:
        _write_immutable(path, _canonical_json(pack) + b"\n", label="evidence pack")
    except DocumentArtifactConflictError as error:
        raise EvidencePackConflictError(str(error)) from error
    return path


def read_evidence_pack(path: str | Path) -> dict[str, Any]:
    return _validate_pack(_strict_json(Path(path).expanduser().resolve(), label="evidence pack"))


def _markdown(memo: dict[str, Any]) -> bytes:
    predictions = ", ".join(memo["prediction_ids"]) if memo["prediction_ids"] else "none"
    references = ", ".join(memo["referenced_ids"]) if memo["referenced_ids"] else "none"
    body = memo["body"].rstrip() + "\n"
    value = (
        "# Research memo\n\n"
        f"- Evidence pack: `{memo['evidence_pack_id']}` ({memo['evidence_pack_sha256']})\n"
        f"- Hypothesis: `{memo['hypothesis_id']}` revision {memo['hypothesis_revision']}\n"
        f"- Decision at: {memo['decision_at']}\n"
        f"- Predictions: {predictions}\n"
        f"- Referenced IDs: {references}\n\n"
        "## Thesis\n\n"
        f"{body}"
    )
    return value.encode("utf-8")


def _validate_memo(memo: dict[str, Any]) -> dict[str, Any]:
    if frozenset(memo) != _MEMO_FIELDS:
        raise EvidencePackValidationError(
            f"memo fields mismatch: missing={sorted(_MEMO_FIELDS - frozenset(memo))}, "
            f"extra={sorted(frozenset(memo) - _MEMO_FIELDS)}"
        )
    if memo["schema_version"] != SCHEMA_VERSION:
        raise EvidencePackValidationError("unsupported memo schema_version")
    _evidence_uuid(memo["memo_id"], field="memo_id")
    _evidence_uuid(memo["evidence_pack_id"], field="evidence_pack_id")
    _evidence_hash(memo["evidence_pack_sha256"], field="evidence_pack_sha256")
    _evidence_uuid(memo["hypothesis_id"], field="hypothesis_id")
    if not isinstance(memo["hypothesis_revision"], int) or memo["hypothesis_revision"] < 1:
        raise EvidencePackValidationError("hypothesis_revision must be positive")
    _evidence_timestamp(memo["decision_at"], field="decision_at")
    if not isinstance(memo["prediction_ids"], list) or any(_evidence_uuid(value, field="prediction_id") is None for value in memo["prediction_ids"]):
        raise EvidencePackValidationError("prediction_ids must be a UUID array")
    if len(set(memo["prediction_ids"])) != len(memo["prediction_ids"]):
        raise EvidencePackValidationError("prediction_ids must be unique")
    if not isinstance(memo["referenced_ids"], list) or any(not isinstance(value, str) or not value.strip() for value in memo["referenced_ids"]):
        raise EvidencePackValidationError("referenced_ids must be a non-empty-string array")
    if len(set(memo["referenced_ids"])) != len(memo["referenced_ids"]):
        raise EvidencePackValidationError("referenced_ids must be unique")
    _evidence_nonempty(memo["body"], field="body")
    _evidence_hash(memo["markdown_sha256"], field="markdown_sha256")
    return memo


def export_research_memo(
    pack: dict[str, Any] | str | Path,
    *,
    hypothesis_id: str,
    hypothesis_revision: int,
    decision_at: datetime | str,
    prediction_ids: list[str] | tuple[str, ...],
    referenced_ids: list[str] | tuple[str, ...],
    body: str,
    memos_root: str | Path = "data/research/memos",
    memo_id: str | None = None,
) -> tuple[Path, Path]:
    """Export one JSON memo and its deterministic Markdown presentation."""

    pack_value = read_evidence_pack(pack) if isinstance(pack, (str, Path)) else _validate_pack(dict(pack))
    decision_text, _ = _evidence_timestamp(decision_at, field="decision_at")
    if decision_text != pack_value["decision_at"]:
        raise EvidencePackValidationError("memo decision_at must equal evidence pack decision_at")
    hypothesis_id = _evidence_uuid(hypothesis_id, field="hypothesis_id")
    assert hypothesis_id is not None
    if not isinstance(hypothesis_revision, int) or hypothesis_revision < 1:
        raise EvidencePackValidationError("hypothesis_revision must be positive")
    normalized_predictions = []
    for index, value in enumerate(prediction_ids):
        parsed = _evidence_uuid(value, field=f"prediction_ids[{index}]")
        assert parsed is not None
        normalized_predictions.append(parsed)
    if len(set(normalized_predictions)) != len(normalized_predictions):
        raise EvidencePackValidationError("prediction_ids must be unique")
    normalized_references = sorted({_evidence_nonempty(value, field="referenced_id") for value in referenced_ids})
    _evidence_nonempty(body, field="body")
    core = {
        "schema_version": SCHEMA_VERSION,
        "memo_id": "",
        "evidence_pack_id": pack_value["pack_id"],
        "evidence_pack_sha256": pack_value["content_sha256"],
        "hypothesis_id": hypothesis_id,
        "hypothesis_revision": hypothesis_revision,
        "decision_at": decision_text,
        "prediction_ids": normalized_predictions,
        "referenced_ids": normalized_references,
        "body": body,
    }
    if memo_id is None:
        memo_id = str(uuid5(_MEMO_NAMESPACE, _canonical_json(core).decode("utf-8")))
    else:
        memo_id = _evidence_uuid(memo_id, field="memo_id")
        assert memo_id is not None
    core["memo_id"] = memo_id
    markdown_hash = _sha256_bytes(_markdown({**core, "markdown_sha256": ""}))
    memo = {**core, "markdown_sha256": markdown_hash}
    _validate_memo(memo)
    root = Path(memos_root).expanduser().resolve()
    json_path = root / f"memo-{memo_id}.json"
    markdown_path = root / f"memo-{memo_id}.md"
    try:
        _write_immutable(markdown_path, _markdown(memo), label="research memo Markdown")
        _write_immutable(json_path, _canonical_json(memo) + b"\n", label="research memo JSON")
    except DocumentArtifactConflictError as error:
        raise EvidencePackConflictError(str(error)) from error
    return json_path, markdown_path


def import_research_memo(
    path: str | Path,
    *,
    evidence_pack: dict[str, Any] | str | Path | None = None,
) -> dict[str, Any]:
    """Verify a JSON/Markdown memo round trip and referenced pack identity."""

    memo_path = Path(path).expanduser().resolve()
    memo = _validate_memo(_strict_json(memo_path, label="research memo"))
    markdown_path = memo_path.with_suffix(".md")
    try:
        markdown = markdown_path.read_bytes()
    except OSError as error:
        raise EvidencePackValidationError(f"research memo Markdown is missing: {markdown_path}") from error
    if _sha256_bytes(markdown) != memo["markdown_sha256"]:
        raise EvidencePackValidationError("research memo Markdown hash mismatch")
    if markdown != _markdown(memo):
        raise EvidencePackValidationError("research memo Markdown is not the deterministic presentation")
    if evidence_pack is not None:
        pack = read_evidence_pack(evidence_pack) if isinstance(evidence_pack, (str, Path)) else _validate_pack(dict(evidence_pack))
        if pack["pack_id"] != memo["evidence_pack_id"] or pack["content_sha256"] != memo["evidence_pack_sha256"]:
            raise EvidencePackValidationError("memo references a different evidence pack")
        if pack["decision_at"] != memo["decision_at"]:
            raise EvidencePackValidationError("memo and evidence pack decision_at differ")
    return memo


__all__ = [
    "ELIGIBILITY_POLICY",
    "SCHEMA_VERSION",
    "EvidencePackConflictError",
    "EvidencePackError",
    "EvidencePackValidationError",
    "build_evidence_pack",
    "export_research_memo",
    "import_research_memo",
    "read_evidence_pack",
]
