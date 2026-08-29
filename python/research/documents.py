"""Immutable research documents and deterministic text-extraction artifacts."""

from __future__ import annotations

import hashlib
import html
import html.parser
import json
import os
import re
import tempfile
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, Final
from uuid import UUID, uuid5

SCHEMA_VERSION: Final[str] = "1.0.0"
EXTRACTOR: Final[str] = "deterministic-text"
EXTRACTOR_VERSION: Final[str] = "1.0.0"

_DOCUMENT_NAMESPACE = UUID("3fd13de4-2d9a-5f93-9836-8c80e0cc7d88")
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_UUID = re.compile(
    r"^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"
)
_MANIFEST_FIELDS = frozenset(
    {
        "schema_version",
        "document_id",
        "source",
        "source_document_id",
        "entity_ids",
        "media_type",
        "source_uri",
        "published_at",
        "available_at",
        "retrieved_at",
        "supersedes_document_id",
        "raw_artifact",
        "text_artifact",
    }
)
_RAW_FIELDS = frozenset({"path", "sha256", "size", "content_type"})
_TEXT_FIELDS = frozenset(
    {
        "status",
        "path",
        "input_sha256",
        "output_sha256",
        "extractor",
        "extractor_version",
        "config",
        "locators",
        "errors",
    }
)
_LOCATOR_FIELDS = frozenset({"ordinal", "page", "section", "start", "end"})
_MEDIA_EXTENSIONS = {
    "text/plain": ".txt",
    "text/html": ".html",
    "application/pdf": ".pdf",
    "application/json": ".json",
}


class DocumentArtifactError(ValueError):
    """Base error for invalid or unsafe document artifacts."""


class DocumentArtifactConflictError(DocumentArtifactError):
    """Raised when an immutable path already contains different bytes."""


class DocumentArtifactValidationError(DocumentArtifactError):
    """Raised when a published document or listed artifact is invalid."""


@dataclass(frozen=True)
class ValidatedDocument:
    manifest_path: Path
    manifest: dict[str, Any]
    raw_path: Path
    text_path: Path | None


def _canonical_json(value: Any) -> bytes:
    return json.dumps(
        value,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")


def _sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise DocumentArtifactValidationError(f"cannot hash artifact {path}: {error}") from error
    return digest.hexdigest()


def _strict_json(path: Path, *, label: str) -> dict[str, Any]:
    try:
        document = json.loads(
            path.read_text(encoding="utf-8"),
            object_pairs_hook=_object_without_duplicates,
            parse_constant=lambda value: (_ for _ in ()).throw(ValueError(f"invalid JSON constant {value}")),
        )
    except (OSError, UnicodeError, ValueError) as error:
        raise DocumentArtifactValidationError(f"invalid {label} {path}: {error}") from error
    if not isinstance(document, dict):
        raise DocumentArtifactValidationError(f"invalid {label} {path}: expected an object")
    return document


def _object_without_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key {key!r}")
        result[key] = value
    return result


def _timestamp(value: datetime | str, *, field: str) -> tuple[str, datetime]:
    if isinstance(value, datetime):
        parsed = value
    elif isinstance(value, str):
        candidate = value[:-1] + "+00:00" if value.endswith("Z") else value
        try:
            parsed = datetime.fromisoformat(candidate)
        except ValueError as error:
            raise DocumentArtifactValidationError(f"{field} must be an RFC 3339 timestamp") from error
    else:
        raise DocumentArtifactValidationError(f"{field} must be an RFC 3339 timestamp")
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        raise DocumentArtifactValidationError(f"{field} must include a timezone")
    parsed = parsed.astimezone(UTC)
    fraction = f".{parsed.microsecond:06d}".rstrip("0") if parsed.microsecond else ""
    return parsed.strftime("%Y-%m-%dT%H:%M:%S") + fraction + "Z", parsed


def _nonempty(value: Any, *, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise DocumentArtifactValidationError(f"{field} must be a non-empty string")
    return value.strip()


def _uuid(value: Any, *, field: str, nullable: bool = False) -> str | None:
    if value is None and nullable:
        return None
    candidate = _nonempty(value, field=field).lower()
    if not _UUID.fullmatch(candidate):
        raise DocumentArtifactValidationError(f"{field} must be a canonical UUID")
    return candidate


def _hash(value: Any, *, field: str, nullable: bool = False) -> str | None:
    if value is None and nullable:
        return None
    candidate = _nonempty(value, field=field)
    if not _SHA256.fullmatch(candidate):
        raise DocumentArtifactValidationError(f"{field} must be a lowercase SHA-256")
    return candidate


def _safe_relative(path: Any, *, field: str) -> str:
    value = _nonempty(path, field=field)
    candidate = Path(value)
    if candidate.is_absolute() or "\\" in value or "\x00" in value or ".." in candidate.parts:
        raise DocumentArtifactValidationError(f"{field} must be a safe relative path")
    if any(part in {"", "."} for part in candidate.parts):
        raise DocumentArtifactValidationError(f"{field} must not contain empty or dot path components")
    return value


def _write_immutable(path: Path, content: bytes, *, label: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        if not path.is_file() or _sha256_file(path) != _sha256_bytes(content):
            raise DocumentArtifactConflictError(f"immutable {label} already contains different bytes: {path}")
        return
    temporary: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(dir=path.parent, prefix=f".{path.name}.", delete=False) as stream:
            temporary = Path(stream.name)
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        try:
            os.link(temporary, path)
        except FileExistsError:
            if not path.is_file() or _sha256_file(path) != _sha256_bytes(content):
                raise DocumentArtifactConflictError(
                    f"immutable {label} was concurrently published with different bytes: {path}"
                )
        finally:
            temporary.unlink(missing_ok=True)
    except OSError as error:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
        raise DocumentArtifactError(f"write immutable {label} {path}: {error}") from error


class _VisibleTextParser(html.parser.HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.parts: list[str] = []
        self._hidden = 0

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        del attrs
        if tag.lower() in {"script", "style", "noscript"}:
            self._hidden += 1
        elif self._hidden == 0 and tag.lower() in {"br", "p", "div", "h1", "h2", "h3", "li", "tr"}:
            self.parts.append("\n")

    def handle_endtag(self, tag: str) -> None:
        lowered = tag.lower()
        if lowered in {"script", "style", "noscript"} and self._hidden:
            self._hidden -= 1
        elif self._hidden == 0 and lowered in {"p", "div", "h1", "h2", "h3", "li", "tr"}:
            self.parts.append("\n")

    def handle_data(self, data: str) -> None:
        if self._hidden == 0:
            self.parts.append(data)


def _normalise_text(text: str) -> str:
    lines = [line.rstrip() for line in text.replace("\r\n", "\n").replace("\r", "\n").splitlines()]
    while lines and not lines[0].strip():
        lines.pop(0)
    while lines and not lines[-1].strip():
        lines.pop()
    return "\n".join(lines) + ("\n" if lines else "")


def _extract_text(raw: bytes, media_type: str) -> tuple[bytes | None, list[dict[str, Any]], list[str]]:
    section_hint: str | None = None
    if media_type == "text/plain":
        try:
            text = raw.decode("utf-8")
        except UnicodeDecodeError as error:
            return None, [], [f"UTF-8 decoding failed at byte {error.start}"]
    elif media_type == "text/html":
        try:
            source = raw.decode("utf-8")
            parser = _VisibleTextParser()
            parser.feed(source)
            parser.close()
            text = "".join(parser.parts)
            heading = re.search(r"<h[1-6][^>]*>(.*?)</h[1-6]>", source, flags=re.IGNORECASE | re.DOTALL)
            if heading is not None:
                section_hint = html.unescape(re.sub(r"<[^>]+>", "", heading.group(1))).strip()
        except (UnicodeDecodeError, ValueError) as error:
            return None, [], [f"HTML decoding failed: {error}"]
    else:
        return None, [], [f"no deterministic extractor is registered for {media_type}"]

    text = _normalise_text(text)
    pages = text.split("\f")
    locators: list[dict[str, Any]] = []
    offset = 0
    for ordinal, page in enumerate(pages):
        start = offset
        end = start + len(page)
        locator: dict[str, Any] = {"ordinal": ordinal, "page": ordinal + 1, "start": start, "end": end}
        for line in page.splitlines():
            stripped = line.strip()
            if stripped.startswith("#"):
                locator["section"] = stripped.lstrip("#").strip()
                break
        if "section" not in locator and ordinal == 0 and section_hint:
            locator["section"] = section_hint
        locators.append(locator)
        offset = end + (1 if ordinal < len(pages) - 1 else 0)
    return text.encode("utf-8"), locators, []


def _validate_locator(value: Any, *, index: int) -> dict[str, Any]:
    if not isinstance(value, dict) or frozenset(value) - _LOCATOR_FIELDS or not {"ordinal", "start", "end"} <= frozenset(value):
        raise DocumentArtifactValidationError(f"text_artifact.locators[{index}] has invalid fields")
    result = dict(value)
    for field in ("ordinal", "start", "end"):
        if not isinstance(result[field], int) or result[field] < 0:
            raise DocumentArtifactValidationError(f"text_artifact.locators[{index}].{field} must be non-negative")
    if "page" in result and (not isinstance(result["page"], int) or result["page"] < 1):
        raise DocumentArtifactValidationError(f"text_artifact.locators[{index}].page must be positive")
    if "section" in result:
        _nonempty(result["section"], field=f"text_artifact.locators[{index}].section")
    if result["end"] < result["start"]:
        raise DocumentArtifactValidationError(f"text_artifact.locators[{index}] has reversed offsets")
    return result


def _validate_manifest(document: dict[str, Any]) -> dict[str, Any]:
    if frozenset(document) != _MANIFEST_FIELDS:
        raise DocumentArtifactValidationError(
            f"document manifest fields mismatch: missing={sorted(_MANIFEST_FIELDS - frozenset(document))}, "
            f"extra={sorted(frozenset(document) - _MANIFEST_FIELDS)}"
        )
    if document["schema_version"] != SCHEMA_VERSION:
        raise DocumentArtifactValidationError("unsupported document schema_version")
    document_id = _uuid(document["document_id"], field="document_id")
    assert document_id is not None
    _nonempty(document["source"], field="source")
    _nonempty(document["source_document_id"], field="source_document_id")
    if not isinstance(document["entity_ids"], list):
        raise DocumentArtifactValidationError("entity_ids must be an array")
    entity_ids = [_uuid(value, field=f"entity_ids[{index}]") for index, value in enumerate(document["entity_ids"])]
    if len(set(entity_ids)) != len(entity_ids):
        raise DocumentArtifactValidationError("entity_ids must be unique")
    media_type = _nonempty(document["media_type"], field="media_type").lower()
    if "/" not in media_type:
        raise DocumentArtifactValidationError("media_type must be a MIME type")
    _nonempty(document["source_uri"], field="source_uri")
    _, available_at = _timestamp(document["available_at"], field="available_at")
    _, retrieved_at = _timestamp(document["retrieved_at"], field="retrieved_at")
    if available_at > retrieved_at:
        raise DocumentArtifactValidationError("available_at must not be after retrieved_at")
    if document["published_at"] is not None:
        _timestamp(document["published_at"], field="published_at")
    _uuid(document["supersedes_document_id"], field="supersedes_document_id", nullable=True)

    raw = document["raw_artifact"]
    if not isinstance(raw, dict) or frozenset(raw) != _RAW_FIELDS:
        raise DocumentArtifactValidationError("raw_artifact fields are invalid")
    _safe_relative(raw["path"], field="raw_artifact.path")
    raw_hash = _hash(raw["sha256"], field="raw_artifact.sha256")
    assert raw_hash is not None
    if not isinstance(raw["size"], int) or raw["size"] < 0:
        raise DocumentArtifactValidationError("raw_artifact.size must be non-negative")
    _nonempty(raw["content_type"], field="raw_artifact.content_type")

    text = document["text_artifact"]
    if not isinstance(text, dict) or frozenset(text) != _TEXT_FIELDS:
        raise DocumentArtifactValidationError("text_artifact fields are invalid")
    status = text["status"]
    if status not in {"extracted", "failed"}:
        raise DocumentArtifactValidationError("text_artifact.status is invalid")
    text_path = text["path"]
    if text_path is not None:
        text_path = _safe_relative(text_path, field="text_artifact.path")
    input_hash = _hash(text["input_sha256"], field="text_artifact.input_sha256")
    assert input_hash is not None
    output_hash = _hash(text["output_sha256"], field="text_artifact.output_sha256", nullable=True)
    _nonempty(text["extractor"], field="text_artifact.extractor")
    _nonempty(text["extractor_version"], field="text_artifact.extractor_version")
    if not isinstance(text["config"], dict):
        raise DocumentArtifactValidationError("text_artifact.config must be an object")
    if not isinstance(text["locators"], list):
        raise DocumentArtifactValidationError("text_artifact.locators must be an array")
    for index, locator in enumerate(text["locators"]):
        _validate_locator(locator, index=index)
    if not isinstance(text["errors"], list) or any(not isinstance(error, str) or not error.strip() for error in text["errors"]):
        raise DocumentArtifactValidationError("text_artifact.errors must be an array of non-empty strings")
    if status == "extracted" and (text_path is None or output_hash is None or text["errors"]):
        raise DocumentArtifactValidationError("extracted text requires path/output hash and no errors")
    if status == "failed" and not text["errors"]:
        raise DocumentArtifactValidationError("failed text extraction requires an error")
    if input_hash != raw_hash:
        raise DocumentArtifactValidationError("text_artifact.input_sha256 does not match raw_artifact.sha256")
    return document


def _default_document_id(source: str, source_document_id: str) -> str:
    return str(uuid5(_DOCUMENT_NAMESPACE, f"{source}\x00{source_document_id}"))


def publish_document(
    raw: bytes,
    *,
    source: str,
    source_document_id: str,
    media_type: str,
    source_uri: str,
    available_at: datetime | str,
    retrieved_at: datetime | str,
    published_at: datetime | str | None = None,
    entity_ids: tuple[str, ...] | list[str] = (),
    supersedes_document_id: str | None = None,
    metadata: dict[str, Any] | None = None,
    documents_root: str | Path = "data/research/documents",
    document_id: str | None = None,
) -> ValidatedDocument:
    """Write raw bytes first, then publish a deterministic text result/manifest."""

    if not isinstance(raw, bytes):
        raise DocumentArtifactValidationError("raw must be bytes")
    source = _nonempty(source, field="source")
    source_document_id = _nonempty(source_document_id, field="source_document_id")
    media_type = _nonempty(media_type, field="media_type").lower()
    source_uri = _nonempty(source_uri, field="source_uri")
    available_text, available = _timestamp(available_at, field="available_at")
    retrieved_text, retrieved = _timestamp(retrieved_at, field="retrieved_at")
    if available > retrieved:
        raise DocumentArtifactValidationError("available_at must not be after retrieved_at")
    published_text = None if published_at is None else _timestamp(published_at, field="published_at")[0]
    document_id = _uuid(document_id, field="document_id", nullable=True) or _default_document_id(source, source_document_id)
    supersedes_document_id = _uuid(supersedes_document_id, field="supersedes_document_id", nullable=True)
    if metadata is None:
        metadata = {}
    if not isinstance(metadata, dict):
        raise DocumentArtifactValidationError("metadata must be an object")
    normalized_entities = tuple(_uuid(value, field=f"entity_ids[{index}]") for index, value in enumerate(entity_ids))
    if len(set(normalized_entities)) != len(normalized_entities):
        raise DocumentArtifactValidationError("entity_ids must be unique")
    raw_hash = _sha256_bytes(raw)
    root = Path(documents_root).expanduser().resolve()
    extension = _MEDIA_EXTENSIONS.get(media_type, ".bin")
    raw_relative = f"document-{document_id}/raw-{raw_hash}{extension}"
    raw_path = root / raw_relative

    # This write is deliberately before _extract_text. A parser failure must
    # retain the exact source bytes and their content address.
    _write_immutable(raw_path, raw, label="raw document")
    text_bytes, locators, errors = _extract_text(raw, media_type)
    text_relative: str | None = None
    output_hash: str | None = None
    if text_bytes is not None:
        output_hash = _sha256_bytes(text_bytes)
        text_relative = f"document-{document_id}/text-{output_hash}.txt"
        _write_immutable(root / text_relative, text_bytes, label="text artifact")

    manifest = {
        "schema_version": SCHEMA_VERSION,
        "document_id": document_id,
        "source": source,
        "source_document_id": source_document_id,
        "entity_ids": list(normalized_entities),
        "media_type": media_type,
        "source_uri": source_uri,
        "published_at": published_text,
        "available_at": available_text,
        "retrieved_at": retrieved_text,
        "supersedes_document_id": supersedes_document_id,
        "raw_artifact": {
            "path": raw_relative,
            "sha256": raw_hash,
            "size": len(raw),
            "content_type": media_type,
        },
        "text_artifact": {
            "status": "extracted" if text_bytes is not None else "failed",
            "path": text_relative,
            "input_sha256": raw_hash,
            "output_sha256": output_hash,
            "extractor": EXTRACTOR,
            "extractor_version": EXTRACTOR_VERSION,
            "config": {"media_type": media_type},
            "locators": locators,
            "errors": errors,
        },
    }
    # Metadata is intentionally kept in the relational document row, where it
    # does not alter the immutable evidence manifest identity.
    del metadata, retrieved
    _validate_manifest(manifest)
    manifest_path = root / f"document-{document_id}" / "manifest.json"
    manifest_bytes = _canonical_json(manifest) + b"\n"
    _write_immutable(manifest_path, manifest_bytes, label="document manifest")
    return read_document(manifest_path, documents_root=root)


def read_document(path: str | Path, *, documents_root: str | Path | None = None) -> ValidatedDocument:
    """Verify the manifest, raw bytes, and extracted text listed by a document."""

    manifest_path = Path(path).expanduser().resolve()
    document = _validate_manifest(_strict_json(manifest_path, label="document manifest"))
    root = Path(documents_root).expanduser().resolve() if documents_root is not None else manifest_path.parent.parent

    def resolve(relative: str, *, field: str) -> Path:
        safe = _safe_relative(relative, field=field)
        candidate = (root / safe).resolve()
        try:
            candidate.relative_to(root)
        except ValueError as error:
            raise DocumentArtifactValidationError(f"{field} escapes documents root") from error
        return candidate

    raw = document["raw_artifact"]
    raw_path = resolve(raw["path"], field="raw_artifact.path")
    if not raw_path.is_file():
        raise DocumentArtifactValidationError(f"raw artifact is missing: {raw_path}")
    if raw_path.stat().st_size != raw["size"] or _sha256_file(raw_path) != raw["sha256"]:
        raise DocumentArtifactValidationError(f"raw artifact hash/size mismatch: {raw_path}")

    text = document["text_artifact"]
    text_path = None
    if text["status"] == "extracted":
        assert text["path"] is not None
        text_path = resolve(text["path"], field="text_artifact.path")
        if not text_path.is_file() or _sha256_file(text_path) != text["output_sha256"]:
            raise DocumentArtifactValidationError(f"text artifact hash mismatch: {text_path}")
        try:
            text_length = len(text_path.read_text(encoding="utf-8"))
        except (OSError, UnicodeError) as error:
            raise DocumentArtifactValidationError(f"cannot read extracted text {text_path}: {error}") from error
        for locator in text["locators"]:
            if locator["end"] > text_length:
                raise DocumentArtifactValidationError(f"text locator exceeds extracted text: {text_path}")
    return ValidatedDocument(manifest_path, document, raw_path, text_path)


def read_document_text(document: ValidatedDocument | str | Path) -> str:
    validated = document if isinstance(document, ValidatedDocument) else read_document(document)
    if validated.text_path is None:
        raise DocumentArtifactValidationError("document has no extracted text artifact")
    try:
        return validated.text_path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        raise DocumentArtifactValidationError(f"cannot read extracted text {validated.text_path}: {error}") from error


__all__ = [
    "EXTRACTOR",
    "EXTRACTOR_VERSION",
    "SCHEMA_VERSION",
    "DocumentArtifactConflictError",
    "DocumentArtifactError",
    "DocumentArtifactValidationError",
    "ValidatedDocument",
    "publish_document",
    "read_document",
    "read_document_text",
]
