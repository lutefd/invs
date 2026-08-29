#!/usr/bin/env python3
"""Prepare deterministic local inputs for the v0.4 research acceptance drill."""

from __future__ import annotations

import argparse
import hashlib
import json
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from research.documents import publish_document, read_document_text
from research.events import (
    build_event_proposal,
    metadata_revision_input,
    review_event_proposal,
    write_event_proposal,
)
from research.evidence import (
    EvidencePackValidationError,
    build_evidence_pack,
    export_research_memo,
    import_research_memo,
    read_evidence_pack,
)
from research.measurement import measure_prediction

DECISION_AT = datetime(2026, 8, 29, 12, 0, tzinfo=UTC)
MEASURED_AT = datetime(2026, 9, 30, 21, 0, tzinfo=UTC)
FUTURE_AVAILABLE_AT = datetime(2026, 9, 1, 12, 0, tzinfo=UTC)
THEME_ID = "10000000-0000-4000-8000-000000000001"
NVIDIA_ENTITY_ID = "20000000-0000-4000-8000-000000000001"
DOCUMENT_ID = "70000000-0000-4000-8000-000000000001"
RAW_ARTIFACT_ID = "70100000-0000-4000-8000-000000000001"
TEXT_ARTIFACT_ID = "70200000-0000-4000-8000-000000000001"
BAD_PROPOSAL_ID = "71000000-0000-4000-8000-000000000001"
CORRECT_PROPOSAL_ID = "71000000-0000-4000-8000-000000000002"
HYPOTHESIS_ID = "72000000-0000-4000-8000-000000000001"
PREDICTION_ID = "73000000-0000-4000-8000-000000000001"
CLI_ROOT = "research/acceptance/v0.4"


def canonical_json(value: Any) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False).encode("utf-8")


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def sha256_file(path: Path) -> str:
    return sha256_bytes(path.read_bytes())


def timestamp(value: datetime) -> str:
    return value.isoformat(timespec="seconds").replace("+00:00", "Z")


def immutable_write(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        if path.read_bytes() != content:
            raise RuntimeError(f"acceptance artifact already contains different bytes: {path}")
        return
    path.write_bytes(content)


def write_json(path: Path, value: Any) -> None:
    immutable_write(path, canonical_json(value) + b"\n")


def db_path(root: Path, path: Path) -> str:
    return f"{CLI_ROOT}/{path.relative_to(root).as_posix()}"


def write_input(root: Path, name: str, value: Any) -> Path:
    path = root / "inputs" / name
    write_json(path, value)
    return path


def ref(kind: str, identifier: str, digest: str, *, available_at: datetime = DECISION_AT, path: str | None = None, locator: str | None = None) -> dict[str, str]:
    result = {"kind": kind, "id": identifier, "sha256": digest, "available_at": timestamp(available_at)}
    if locator is not None:
        result["locator"] = locator
    if path is not None:
        result["path"] = path
    return result


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--snapshot", type=Path, required=True)
    args = parser.parse_args()
    root = args.root.expanduser().resolve()
    snapshot_path = args.snapshot.expanduser().resolve()
    snapshot = json.loads(snapshot_path.read_text(encoding="utf-8"))
    if snapshot["theme_id"] != THEME_ID or snapshot["decision_at"] != timestamp(DECISION_AT):
        raise RuntimeError("theme snapshot does not match the v0.4 acceptance cutoff")
    if len(snapshot["themes"]) != 7 or len(snapshot["relationships"]) != 6:
        raise RuntimeError("theme snapshot does not contain the reviewed AI-infrastructure graph")

    review_source = (root / "source" / "ai-infrastructure-review.txt")
    immutable_write(review_source, (Path("/tmp/research-fixtures/ai-infrastructure-review.txt").read_bytes()))
    document = publish_document(
        review_source.read_bytes(),
        source="fixture-sec",
        source_document_id="ai-infrastructure-filing-2026-08-29",
        media_type="text/plain",
        source_uri="https://example.test/filings/ai-infrastructure-2026-08-29.txt",
        published_at=datetime(2026, 8, 29, 11, 55, tzinfo=UTC),
        available_at=DECISION_AT,
        retrieved_at=datetime(2026, 8, 29, 12, 5, tzinfo=UTC),
        entity_ids=[NVIDIA_ENTITY_ID],
        metadata={"fixture": True, "review": "human"},
        documents_root=root / "documents",
        document_id=DOCUMENT_ID,
    )
    manifest = document.manifest
    text = read_document_text(document)
    excerpt = "Compute demand depends on advanced semiconductor manufacturing and packaging,"
    start = text.index(excerpt)
    source_span = {
        "locator": f"chars:{start}-{start + len(excerpt)}",
        "text_sha256": sha256_bytes(excerpt.encode("utf-8")),
        "excerpt": excerpt,
    }

    bad_proposal = build_event_proposal(
        document,
        event_type="capex_guidance",
        payload={"direction": "down", "change": "decreased", "confidence_note": "fixture intentionally wrong"},
        source_spans=[source_span],
        prompt_version="fixture-prompt-v1",
        model="fixture-model-v1",
        parameters={"temperature": 0},
        confidence=0.99,
        proposal_id=BAD_PROPOSAL_ID,
        proposed_at=DECISION_AT,
    )
    bad_rejected = review_event_proposal(
        bad_proposal,
        status="rejected",
        reviewer_id="researcher",
        note="Rejected: source says demand depends on the listed bottlenecks; it does not support a decrease claim.",
        reviewed_at=DECISION_AT,
    )
    correct_proposal = build_event_proposal(
        document,
        event_type="theme_exposure",
        payload={"direction": "up", "exposure": "compute-demand-bottleneck", "confidence_note": "fixture reviewed"},
        source_spans=[source_span],
        prompt_version="fixture-prompt-v1",
        model="fixture-model-v1",
        parameters={"temperature": 0},
        confidence=0.86,
        proposal_id=CORRECT_PROPOSAL_ID,
        proposed_at=DECISION_AT,
    )
    correct_accepted = review_event_proposal(
        correct_proposal,
        status="accepted",
        reviewer_id="researcher",
        note="Accepted: the source span supports the bounded theme-exposure claim.",
        reviewed_at=DECISION_AT,
    )
    bad_rejected_path = write_event_proposal(bad_rejected, events_root=root / "events")
    correct_accepted_path = write_event_proposal(correct_accepted, events_root=root / "events")

    snapshot_digest = sha256_file(snapshot_path)
    macro_path = root / "canonical" / "macro-ai-demand.json"
    company_path = root / "canonical" / "company-nvidia-exposure.json"
    feature_path = root / "features" / "ai-infrastructure-feature-snapshot.json"
    write_json(macro_path, {"artifact_id": "macro-ai-demand-2026-08-29", "series": "ai-capex-guidance", "value": "deployment-led"})
    write_json(company_path, {"artifact_id": "company-nvidia-exposure-2026-08-29", "entity_id": NVIDIA_ENTITY_ID, "exposure": "accelerator-compute"})
    write_json(feature_path, {"artifact_id": "feature-ai-infrastructure-2026-08-29", "feature_set": "market-momentum", "feature_set_version": "1.0.0", "value": "bounded-fixture"})

    theme_ref = ref("theme-snapshot", THEME_ID, snapshot_digest, path=db_path(root, snapshot_path), locator="decision_at=2026-08-29T12:00:00Z")
    macro_ref = ref("macro-observation", "macro-ai-demand-2026-08-29", sha256_file(macro_path), path=db_path(root, macro_path), locator="series=ai-capex-guidance")
    company_ref = ref("company-observation", "company-nvidia-exposure-2026-08-29", sha256_file(company_path), path=db_path(root, company_path), locator="entity_id=" + NVIDIA_ENTITY_ID)
    feature_ref = ref("feature-artifact", "feature-ai-infrastructure-2026-08-29", sha256_file(feature_path), path=db_path(root, feature_path), locator="feature_set=market-momentum@1.0.0")
    document_ref = ref("document", DOCUMENT_ID, manifest["raw_artifact"]["sha256"], path=db_path(root, document.raw_path), locator="manifest=" + db_path(root, document.manifest_path))
    event_ref = ref("event-proposal", CORRECT_PROPOSAL_ID, sha256_file(correct_accepted_path), path=db_path(root, correct_accepted_path), locator="revision=2;status=accepted")
    pack_path = build_evidence_pack(
        decision_at=DECISION_AT,
        created_at=DECISION_AT,
        theme_refs=[theme_ref],
        canonical_observation_refs=[macro_ref, company_ref],
        feature_artifact_refs=[feature_ref],
        document_refs=[document_ref],
        event_proposal_refs=[event_ref],
        packs_root=root / "evidence-packs",
    )
    pack = read_evidence_pack(pack_path)

    future_rejected = False
    try:
        build_evidence_pack(
            decision_at=DECISION_AT,
            theme_refs=[theme_ref],
            document_refs=[ref("later-filing", "filing-after-decision", "b" * 64, available_at=FUTURE_AVAILABLE_AT)],
            packs_root=root / "future-packs",
        )
    except EvidencePackValidationError:
        future_rejected = True
    if not future_rejected:
        raise RuntimeError("future filing reference was accepted into an earlier evidence pack")

    write_json(root / "price-artifact.json", {
        "schema_version": "1.0.0",
        "artifact_id": "fixture-prices-2026-09",
        "available_at": timestamp(MEASURED_AT),
        "rows": [
            {"asset_id": "asset-a", "observed_at": "2026-08-29T21:00:00Z", "close": "100"},
            {"asset_id": "asset-a", "observed_at": timestamp(MEASURED_AT), "close": "120"},
            {"asset_id": "asset-b", "observed_at": "2026-08-29T21:00:00Z", "close": "80"},
            {"asset_id": "asset-b", "observed_at": timestamp(MEASURED_AT), "close": "88"},
            {"asset_id": "benchmark", "observed_at": "2026-08-29T21:00:00Z", "close": "200"},
            {"asset_id": "benchmark", "observed_at": timestamp(MEASURED_AT), "close": "210"},
        ],
    })
    price_path = root / "price-artifact.json"
    price_digest = sha256_file(price_path)

    hypothesis_input = {
        "id": HYPOTHESIS_ID,
        "title": "AI infrastructure bottleneck exposure",
        "status": "active",
    }
    hypothesis_revision_input = {
        "hypothesis_id": HYPOTHESIS_ID,
        "revision": 1,
        "thesis": "AI deployment growth should benefit a bounded basket of compute and infrastructure exposures while bottlenecks remain binding.",
        "causal_model": {"demand": "AI deployment growth", "mechanism": "capacity bottlenecks", "beneficiaries": "reviewed theme exposures"},
        "horizon": "through 2026-09-30",
        "benchmark": "benchmark",
        "universe": ["asset-a", "asset-b"],
        "invalidation_conditions": ["Demand becomes inventory-led rather than deployment-led.", "Power interconnection delays prevent commissioning."],
        "decision_at": timestamp(DECISION_AT),
        "review_at": timestamp(MEASURED_AT),
        "evidence_pack_id": pack["pack_id"],
        "evidence_pack_sha256": pack["content_sha256"],
        "created_at": timestamp(DECISION_AT),
    }
    hypothesis_evidence_input = {
        "hypothesis_id": HYPOTHESIS_ID,
        "hypothesis_revision": 1,
        "evidence_ref": {"kind": "evidence-pack", "id": pack["pack_id"], "sha256": pack["content_sha256"]},
        "direction": "supports",
        "weight": 1.0,
        "note": "The decision-date pack contains the reviewed theme, eligible inputs, and accepted event.",
        "available_at": timestamp(DECISION_AT),
    }
    prediction_input = {
        "id": PREDICTION_ID,
        "hypothesis_id": HYPOTHESIS_ID,
        "hypothesis_revision": 1,
        "asset_or_universe": ["asset-a", "asset-b"],
        "expected_direction": "relative_outperformance",
        "expected_range": {"relative_return_min": "0.05"},
        "horizon": "through 2026-09-30",
        "confidence": 0.72,
        "created_at": timestamp(DECISION_AT),
    }
    freeze_input = {"prediction_id": PREDICTION_ID, "frozen_at": timestamp(DECISION_AT)}
    outcome = measure_prediction(
        {**prediction_input, "status": "frozen"},
        price_artifact=price_path,
        price_artifact_sha256=price_digest,
        benchmark_asset_id="benchmark",
        measured_at=MEASURED_AT,
        input_artifact_refs=[{"kind": "price-artifact", "id": "fixture-prices-2026-09", "sha256": price_digest}],
    )
    outcome_input = {key: value for key, value in outcome.items() if key != "schema_version"}

    write_input(root, "document.json", {
        "id": DOCUMENT_ID,
        "source": manifest["source"],
        "source_document_id": manifest["source_document_id"],
        "media_type": manifest["media_type"],
        "source_uri": manifest["source_uri"],
        "published_at": manifest["published_at"],
        "available_at": manifest["available_at"],
        "retrieved_at": manifest["retrieved_at"],
        "metadata": {"fixture": True, "review": "human"},
    })
    write_input(root, "document-link.json", {"document_id": DOCUMENT_ID, "entity_id": NVIDIA_ENTITY_ID, "relation_kind": "subject", "recorded_at": timestamp(DECISION_AT)})
    write_input(root, "raw-artifact.json", {"artifact_id": RAW_ARTIFACT_ID, "document_id": DOCUMENT_ID, "path": db_path(root, document.raw_path), "sha256": manifest["raw_artifact"]["sha256"], "size_bytes": manifest["raw_artifact"]["size"], "content_type": manifest["raw_artifact"]["content_type"]})
    write_input(root, "text-artifact.json", {"artifact_id": TEXT_ARTIFACT_ID, "document_id": DOCUMENT_ID, "status": manifest["text_artifact"]["status"], "path": db_path(root, document.text_path), "input_sha256": manifest["text_artifact"]["input_sha256"], "output_sha256": manifest["text_artifact"]["output_sha256"], "extractor": manifest["text_artifact"]["extractor"], "extractor_version": manifest["text_artifact"]["extractor_version"], "config": manifest["text_artifact"]["config"], "locators": manifest["text_artifact"]["locators"], "errors": manifest["text_artifact"]["errors"]})
    write_input(root, "bad-proposal.json", {"id": BAD_PROPOSAL_ID})
    write_input(root, "correct-proposal.json", {"id": CORRECT_PROPOSAL_ID})
    write_input(root, "bad-event-revision.json", metadata_revision_input(bad_proposal))
    write_input(root, "bad-event-rejected.json", metadata_revision_input(bad_rejected))
    write_input(root, "correct-event-revision.json", metadata_revision_input(correct_proposal))
    write_input(root, "correct-event-accepted.json", metadata_revision_input(correct_accepted))
    invalid_review = metadata_revision_input(correct_accepted)
    invalid_review.update({"reviewer_id": "", "review_method": "", "reviewed_at": None, "review_note": ""})
    write_input(root, "invalid-review.json", invalid_review)
    write_input(root, "evidence-pack.json", {"id": pack["pack_id"], "decision_at": pack["decision_at"], "path": db_path(root, pack_path), "content_sha256": pack["content_sha256"], "created_at": pack["created_at"]})
    write_input(root, "hypothesis.json", hypothesis_input)
    write_input(root, "hypothesis-revision.json", hypothesis_revision_input)
    write_input(root, "hypothesis-evidence.json", hypothesis_evidence_input)
    write_input(root, "prediction.json", prediction_input)
    write_input(root, "freeze.json", freeze_input)
    write_input(root, "outcome.json", outcome_input)
    memo_json, memo_markdown = export_research_memo(
        pack_path,
        hypothesis_id=HYPOTHESIS_ID,
        hypothesis_revision=1,
        decision_at=DECISION_AT,
        prediction_ids=[PREDICTION_ID],
        referenced_ids=[THEME_ID, DOCUMENT_ID, CORRECT_PROPOSAL_ID, pack["pack_id"]],
        body=hypothesis_revision_input["thesis"] + "\n\nInvalidation: " + "; ".join(hypothesis_revision_input["invalidation_conditions"]),
        memos_root=root / "memos",
    )
    imported_memo = import_research_memo(memo_json, evidence_pack=pack_path)
    if imported_memo["memo_id"] != json.loads(memo_json.read_text(encoding="utf-8"))["memo_id"]:
        raise RuntimeError("memo import did not preserve memo identity")

    summary = {
        "decision_at": timestamp(DECISION_AT),
        "measured_at": timestamp(MEASURED_AT),
        "theme_id": THEME_ID,
        "document_id": DOCUMENT_ID,
        "document_manifest_path": db_path(root, document.manifest_path),
        "document_manifest_sha256": sha256_file(document.manifest_path),
        "raw_artifact_sha256": manifest["raw_artifact"]["sha256"],
        "text_artifact_sha256": manifest["text_artifact"]["output_sha256"],
        "bad_proposal_id": BAD_PROPOSAL_ID,
        "bad_rejected_path": db_path(root, bad_rejected_path),
        "correct_proposal_id": CORRECT_PROPOSAL_ID,
        "correct_accepted_path": db_path(root, correct_accepted_path),
        "correct_accepted_sha256": sha256_file(correct_accepted_path),
        "evidence_pack_id": pack["pack_id"],
        "evidence_pack_sha256": pack["content_sha256"],
        "evidence_pack_path": db_path(root, pack_path),
        "memo_id": imported_memo["memo_id"],
        "memo_json_path": db_path(root, memo_json),
        "memo_markdown_path": db_path(root, memo_markdown),
        "hypothesis_id": HYPOTHESIS_ID,
        "prediction_id": PREDICTION_ID,
        "price_artifact_path": db_path(root, price_path),
        "price_artifact_sha256": price_digest,
        "outcome_id": outcome["outcome_id"],
        "outcome_policy": outcome["measurement_policy_version"],
        "future_reference_rejected": future_rejected,
    }
    write_json(root / "acceptance-artifacts.json", summary)
    print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
