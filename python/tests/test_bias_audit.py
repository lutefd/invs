from __future__ import annotations

import copy
import hashlib
import json
from pathlib import Path

import pytest

from research.bias_audit import (
    BiasAuditValidationError,
    publish_bias_audit,
    validate_bias_audit,
)
from research.bias_audit_cli import main as bias_audit_cli_main

US_CATEGORIES = (
    "identity",
    "membership",
    "calendar",
    "price",
    "macro_vintage",
    "corporate_action",
    "filing",
)
BR_CATEGORIES = ("identity", "membership", "calendar", "price", "corporate_action", "fx")


def _write_spec(tmp_path: Path) -> Path:
    evidence = tmp_path / "evidence.json"
    evidence.write_text('{"source":"fixture"}\n', encoding="utf-8")
    evidence_hash = hashlib.sha256(evidence.read_bytes()).hexdigest()
    datasets = []
    probes = []
    boundary = "2026-01-02T03:04:05.000001Z"
    before = "2026-01-02T03:04:05Z"
    after = "2026-01-02T03:04:05.000002Z"
    for region, categories in (("US", US_CATEGORIES), ("BR", BR_CATEGORIES)):
        for category in categories:
            dataset_id = f"{region.lower()}-{category}"
            kind = "availability_transition"
            classification = "backtest_safe"
            states = ((False, "absent"), (True, "eligible"), (True, "eligible"))
            if region == "BR" and category == "price":
                kind = "blocked_before_receipt"
                classification = "installation_replay_only"
            elif region == "BR" and category == "corporate_action":
                kind = "unsupported_blocks"
                classification = "installation_replay_only"
                states = ((False, "absent"), (False, "unsupported"), (False, "unsupported"))
            datasets.append(
                {
                    "id": dataset_id,
                    "classification": classification,
                    "availability_policy": "exact fixture availability",
                    "scope_decision": "bounded fixture only",
                }
            )
            probes.append(
                {
                    "id": f"{dataset_id}-boundary",
                    "region": region,
                    "category": category,
                    "dataset_id": dataset_id,
                    "kind": kind,
                    "boundary_at": boundary,
                    "before": {
                        "decision_at": before,
                        "eligible": states[0][0],
                        "state": states[0][1],
                    },
                    "at": {
                        "decision_at": boundary,
                        "eligible": states[1][0],
                        "state": states[1][1],
                    },
                    "after": {
                        "decision_at": after,
                        "eligible": states[2][0],
                        "state": states[2][1],
                    },
                    "evidence_artifact_ids": ["fixture-evidence"],
                }
            )
    spec = {
        "schema_version": "1.0.0",
        "audit_id": "bounded-us-br-fixture",
        "git_commit": "a" * 40,
        "datasets": datasets,
        "artifacts": [{"id": "fixture-evidence", "path": evidence.name, "sha256": evidence_hash}],
        "probes": probes,
    }
    path = tmp_path / "audit-spec.json"
    path.write_text(json.dumps(spec, indent=2) + "\n", encoding="utf-8")
    return path


def _mutate_spec(path: Path, mutation) -> None:
    document = json.loads(path.read_text(encoding="utf-8"))
    mutation(document)
    path.write_text(json.dumps(document, indent=2) + "\n", encoding="utf-8")


def test_publish_validate_and_exact_replay(tmp_path: Path) -> None:
    spec = _write_spec(tmp_path)
    first = publish_bias_audit(spec, audits_root=tmp_path / "audits")
    first_bytes = first.read_bytes()
    second = publish_bias_audit(spec, audits_root=tmp_path / "audits")

    assert first == second
    assert second.read_bytes() == first_bytes
    artifact = validate_bias_audit(first)
    assert artifact.manifest["status"] == "passed"
    assert artifact.manifest["regions"] == ["BR", "US"]
    assert artifact.manifest["probe_count"] == len(US_CATEGORIES) + len(BR_CATEGORIES)
    assert artifact.manifest["dataset_classifications"]["br-price"] == "installation_replay_only"


def test_cli_publish_and_validate(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    spec = _write_spec(tmp_path)
    audits_root = tmp_path / "audits"
    assert (
        bias_audit_cli_main(["publish", "--spec", str(spec), "--audits-root", str(audits_root)])
        == 0
    )
    published = json.loads(capsys.readouterr().out)
    assert published["status"] == "passed"
    assert published["verified_artifact_count"] == 1
    assert bias_audit_cli_main(["validate", "--manifest", published["manifest_path"]]) == 0
    validated = json.loads(capsys.readouterr().out)
    assert validated["artifact_id"] == published["artifact_id"]


def test_rejects_missing_required_category(tmp_path: Path) -> None:
    spec = _write_spec(tmp_path)

    def mutation(document: dict[str, object]) -> None:
        probes = document["probes"]
        assert isinstance(probes, list)
        probes[:] = [probe for probe in probes if probe["id"] != "us-membership-boundary"]

    _mutate_spec(spec, mutation)
    with pytest.raises(BiasAuditValidationError, match="missing required categories.*membership"):
        publish_bias_audit(spec, audits_root=tmp_path / "audits")


def test_rejects_installation_replay_relabelled_as_safe(tmp_path: Path) -> None:
    spec = _write_spec(tmp_path)

    def mutation(document: dict[str, object]) -> None:
        datasets = document["datasets"]
        assert isinstance(datasets, list)
        next(item for item in datasets if item["id"] == "br-price")["classification"] = (
            "backtest_safe"
        )

    _mutate_spec(spec, mutation)
    with pytest.raises(BiasAuditValidationError, match="installation_replay_only"):
        publish_bias_audit(spec, audits_root=tmp_path / "audits")


def test_rejects_current_universe_substitution(tmp_path: Path) -> None:
    spec = _write_spec(tmp_path)

    def mutation(document: dict[str, object]) -> None:
        datasets = document["datasets"]
        assert isinstance(datasets, list)
        next(item for item in datasets if item["id"] == "us-membership")["classification"] = (
            "current_research_only"
        )

    _mutate_spec(spec, mutation)
    with pytest.raises(BiasAuditValidationError, match="cannot claim a safe transition"):
        publish_bias_audit(spec, audits_root=tmp_path / "audits")


def test_rejects_lookahead_at_before_boundary(tmp_path: Path) -> None:
    spec = _write_spec(tmp_path)

    def mutation(document: dict[str, object]) -> None:
        probes = document["probes"]
        assert isinstance(probes, list)
        next(item for item in probes if item["id"] == "us-filing-boundary")["before"][
            "eligible"
        ] = True

    _mutate_spec(spec, mutation)
    with pytest.raises(BiasAuditValidationError, match="before must be eligible=False"):
        publish_bias_audit(spec, audits_root=tmp_path / "audits")


def test_rejects_non_exact_mutation_clock(tmp_path: Path) -> None:
    spec = _write_spec(tmp_path)

    def mutation(document: dict[str, object]) -> None:
        probes = document["probes"]
        assert isinstance(probes, list)
        next(item for item in probes if item["id"] == "br-fx-boundary")["after"]["decision_at"] = (
            "2026-01-02T03:04:05.000003Z"
        )

    _mutate_spec(spec, mutation)
    with pytest.raises(BiasAuditValidationError, match="exactly one microsecond"):
        publish_bias_audit(spec, audits_root=tmp_path / "audits")


def test_rejects_unsupported_action_claimed_eligible(tmp_path: Path) -> None:
    spec = _write_spec(tmp_path)

    def mutation(document: dict[str, object]) -> None:
        probes = document["probes"]
        assert isinstance(probes, list)
        next(item for item in probes if item["id"] == "br-corporate_action-boundary")["at"] = {
            "decision_at": "2026-01-02T03:04:05.000001Z",
            "eligible": True,
            "state": "eligible",
        }

    _mutate_spec(spec, mutation)
    with pytest.raises(BiasAuditValidationError, match="at must be eligible=False"):
        publish_bias_audit(spec, audits_root=tmp_path / "audits")


def test_validation_detects_evidence_tampering(tmp_path: Path) -> None:
    spec = _write_spec(tmp_path)
    manifest = publish_bias_audit(spec, audits_root=tmp_path / "audits")
    (tmp_path / "evidence.json").write_text('{"source":"changed"}\n', encoding="utf-8")
    with pytest.raises(BiasAuditValidationError, match="hash mismatch"):
        validate_bias_audit(manifest)


def test_validation_detects_manifest_tampering(tmp_path: Path) -> None:
    spec = _write_spec(tmp_path)
    manifest = publish_bias_audit(spec, audits_root=tmp_path / "audits")
    document = json.loads(manifest.read_text(encoding="utf-8"))
    changed = copy.deepcopy(document)
    changed["probe_count"] -= 1
    manifest.write_text(json.dumps(changed, indent=2) + "\n", encoding="utf-8")
    with pytest.raises(BiasAuditValidationError, match="probe_count"):
        validate_bias_audit(manifest)
