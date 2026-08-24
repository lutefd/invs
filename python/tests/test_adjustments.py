from __future__ import annotations

import hashlib
import json
from pathlib import Path

import pyarrow as pa
import pytest
from pyarrow import parquet

from research.adjustment_cli import main as adjustment_cli_main
from research.adjustments import (
    AdjustmentArtifactError,
    AdjustmentArtifactValidationError,
    publish_adjusted_prices,
    validate_adjustment_artifact,
)

SECURITY_ID = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
SOURCE_ID = "5d6ac836-54fd-4df2-a745-0744180420db"
RUN_ID = "c7286917-ce45-4879-834f-fc975c80c49e"


def _write_raw_prices(root: Path) -> Path:
    directory = root / "normalized" / "prices" / "source=fixture" / f"security_id={SECURITY_ID}"
    directory.mkdir(parents=True)
    rows = []
    for index, (date, close, volume) in enumerate(
        (
            ("2025-01-05", "100", "100"),
            ("2025-01-06", "102", "101"),
            ("2025-01-07", "104", "102"),
            ("2025-01-08", "106", "103"),
        )
    ):
        rows.append(
            {
                "schema_version": "1.0.0",
                "source": "fixture",
                "security_id": SECURITY_ID,
                "interval": "1d",
                "price_basis": "raw",
                "currency": "USD",
                "observed_at": f"{date}T21:00:00Z",
                "available_at": f"{date}T22:00:00Z",
                "open": close,
                "high": close,
                "low": close,
                "close": close,
                "volume": volume,
                "has_volume": True,
                "raw_payload_hash": str(index + 1) * 64,
            }
        )
    temporary = directory / "pending.parquet"
    parquet.write_table(pa.Table.from_pylist(rows), temporary, compression="NONE")
    digest = hashlib.sha256(temporary.read_bytes()).hexdigest()
    part = directory / f"part-{digest}.parquet"
    temporary.rename(part)
    manifest = {
        "manifest_version": 1,
        "schema_version": "1.0.0",
        "normalizer_version": "fixture-v1",
        "git_commit": "0" * 40,
        "source": "fixture",
        "data_source_id": SOURCE_ID,
        "ingestion_run_id": RUN_ID,
        "partition": {
            "dataset": "prices",
            "source": "fixture",
            "security_id": SECURITY_ID,
        },
        "row_count": len(rows),
        "parts": [{"path": part.name, "sha256": digest, "row_count": len(rows)}],
    }
    path = directory / "manifest.json"
    path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    return path


def _action(
    *,
    action_id: str,
    event: str,
    kind: str,
    observed: str,
    ratio: tuple[str, str] | None = None,
    cash: str | None = None,
    status: str = "active",
    available: str = "2025-01-04T00:00:00Z",
) -> dict[str, object]:
    return {
        "schema_version": "2.0.0",
        "id": action_id,
        "security_id": SECURITY_ID,
        "source_event_id": event,
        "revision": 0,
        "action_status": status,
        "action_type": kind,
        "observed_at": f"{observed}T00:00:00Z",
        "observed_precision": "date",
        "published_at": "2025-01-03T00:00:00Z",
        "published_precision": "date",
        "available_at": available,
        "effective_at": f"{observed}T00:00:00Z",
        "effective_precision": "date",
        "record_date": None,
        "payment_date": None,
        "ratio_numerator": ratio[0] if ratio else None,
        "ratio_denominator": ratio[1] if ratio else None,
        "cash_amount": cash,
        "currency": "USD" if cash is not None else None,
        "target_security_id": None,
        "source_reference": f"https://example.test/{event}",
        "recorded_at": "2025-01-04T00:02:00Z",
        "provenance": {
            "data_source_id": SOURCE_ID,
            "ingestion_run_id": RUN_ID,
            "raw_payload_hash": "a" * 64,
            "raw_record_locator": event,
            "ingested_at": "2025-01-04T00:01:00Z",
            "normalizer_version": "fixture-action-v1",
        },
    }


def _actions() -> list[dict[str, object]]:
    return [
        _action(
            action_id="11111111-1111-4111-8111-111111111111",
            event="fixture/split",
            kind="split",
            observed="2025-01-08",
            ratio=("2", "1"),
        ),
        _action(
            action_id="22222222-2222-4222-8222-222222222222",
            event="fixture/dividend",
            kind="cash_dividend",
            observed="2025-01-07",
            cash="2",
        ),
    ]


def test_adjustment_artifact_pins_inputs_and_applies_exact_factors(tmp_path: Path) -> None:
    raw_manifest = _write_raw_prices(tmp_path / "data")
    adjusted_root = tmp_path / "adjusted"
    path = publish_adjusted_prices(
        raw_manifest,
        reversed(_actions()),
        decision_at="2025-01-09T00:00:00Z",
        adjustments_root=adjusted_root,
    )
    artifact = validate_adjustment_artifact(path)
    manifest = artifact.manifest
    rows = artifact.rows

    assert manifest["raw_price_basis"] == "raw"
    assert manifest["raw_price_manifest_sha256"] == hashlib.sha256(
        raw_manifest.read_bytes()
    ).hexdigest()
    assert [row["source_event_id"] for row in manifest["selected_actions"]] == [
        "fixture/dividend",
        "fixture/split",
    ]
    assert rows[1]["adjusted_close"] == "50"
    assert rows[2]["adjusted_close"] == "52"
    assert rows[2]["adjusted_volume"] == "204"
    assert rows[3]["adjusted_close"] == "106"
    assert rows[3]["adjusted_volume"] == "103"
    assert rows[0]["price_factor"].startswith("0.490196078431372549")
    assert rows[0]["raw_close"] == "100"

    raw_hash = hashlib.sha256(raw_manifest.read_bytes()).hexdigest()
    replay = publish_adjusted_prices(
        raw_manifest,
        _actions(),
        decision_at="2025-01-09T00:00:00Z",
        adjustments_root=adjusted_root,
    )
    assert replay == path
    assert hashlib.sha256(raw_manifest.read_bytes()).hexdigest() == raw_hash


def test_unsupported_and_not_yet_knowable_actions_write_nothing(tmp_path: Path) -> None:
    raw_manifest = _write_raw_prices(tmp_path / "data")
    root = tmp_path / "adjusted"
    unsupported = _action(
        action_id="33333333-3333-4333-8333-333333333333",
        event="fixture/rights",
        kind="rights_issue",
        observed="2025-01-08",
        status="unsupported",
    )
    with pytest.raises(AdjustmentArtifactError, match="blocks adjustment"):
        publish_adjusted_prices(
            raw_manifest,
            [unsupported],
            decision_at="2025-01-09T00:00:00Z",
            adjustments_root=root,
        )
    assert not root.exists()

    future_knowledge = _actions()[0]
    future_knowledge["available_at"] = "2025-01-10T00:00:00Z"
    with pytest.raises(AdjustmentArtifactError, match="not knowable"):
        publish_adjusted_prices(
            raw_manifest,
            [future_knowledge],
            decision_at="2025-01-09T00:00:00Z",
            adjustments_root=root,
        )
    assert not root.exists()


def test_adjustment_validation_detects_tampering_and_cli_round_trip(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    raw_manifest = _write_raw_prices(tmp_path / "data")
    actions_path = tmp_path / "actions.json"
    actions_path.write_text(json.dumps({"actions": _actions()}), encoding="utf-8")
    root = tmp_path / "adjusted"
    assert adjustment_cli_main(
        [
            "publish",
            "--raw-manifest",
            str(raw_manifest),
            "--actions",
            str(actions_path),
            "--decision-at",
            "2025-01-09T00:00:00Z",
            "--adjustments-root",
            str(root),
        ]
    ) == 0
    summary = json.loads(capsys.readouterr().out)
    manifest_path = Path(summary["manifest_path"])
    assert adjustment_cli_main(["validate", "--manifest", str(manifest_path)]) == 0
    capsys.readouterr()

    artifact = validate_adjustment_artifact(manifest_path)
    artifact.part_path.write_bytes(artifact.part_path.read_bytes() + b"tampered")
    with pytest.raises(AdjustmentArtifactValidationError, match="hash mismatch"):
        validate_adjustment_artifact(manifest_path)
