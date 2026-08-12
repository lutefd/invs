#!/usr/bin/env python3
"""Focused, dependency-free contract checks for the deterministic feature schemas."""

from __future__ import annotations

import hashlib
import json
import re
import sys
from datetime import datetime, timedelta
from pathlib import Path


ROOT = Path(__file__).resolve().parent
DECIMAL = re.compile(r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$")
SHA256 = re.compile(r"^[0-9a-f]{64}$")
UTC = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z$")


def load_json(name: str) -> dict:
    return json.loads((ROOT / name).read_text(encoding="utf-8"))


def parse_utc(value: str) -> datetime:
    assert UTC.fullmatch(value), f"not a canonical UTC timestamp: {value!r}"
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def input_fingerprint(manifest: dict) -> str:
    envelope = {
        "decision_at": manifest["decision_at"],
        "feature_set": manifest["feature_set"],
        "feature_set_version": manifest["feature_set_version"],
        "selected_input_manifests": sorted(
            manifest["selected_input_manifests"],
            key=lambda item: (item["path"], item["sha256"]),
        ),
        "selected_input_parts": sorted(
            manifest["selected_input_parts"],
            key=lambda item: (item["path"], item["sha256"]),
        ),
    }
    canonical = json.dumps(
        envelope,
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")
    return hashlib.sha256(canonical).hexdigest()


def assert_no_unknown_keys(document: dict, schema: dict, label: str) -> None:
    assert set(document) <= set(schema["properties"]), f"{label} has an unknown property"
    missing = set(schema["required"]) - set(document)
    assert not missing, f"{label} is missing required properties: {sorted(missing)}"


def assert_fixed_metadata(document: dict, schema: dict, label: str) -> None:
    assert_no_unknown_keys(document, schema, label)
    for name, property_schema in schema["properties"].items():
        if "const" in property_schema and name in document:
            assert document[name] == property_schema["const"], (
                f"{label}.{name} is not the supported contract value"
            )


def assert_artifact(artifact: dict, schema: dict) -> None:
    artifact_schema = schema["$defs"]["artifactMetadata"]
    assert_fixed_metadata(artifact, artifact_schema, "artifact")
    assert re.fullmatch(
        r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",
        artifact["artifact_id"],
    )
    assert artifact["git_commit"] == "unknown" or re.fullmatch(
        r"[0-9a-f]{40}", artifact["git_commit"]
    )
    parse_utc(artifact["created_at"])


def assert_timing(document: dict) -> None:
    decision_at = parse_utc(document["decision_at"])
    input_available_at = parse_utc(document["input_available_at"])
    available_at = parse_utc(document["available_at"])
    assert input_available_at <= decision_at
    assert available_at == input_available_at + timedelta(
        seconds=document["computation_delay_seconds"]
    )


def assert_observation(document: dict, schema: dict) -> None:
    assert_fixed_metadata(document, schema, "feature observation")
    assert_artifact(document["artifact"], load_json("feature-manifest.schema.json"))
    parse_utc(document["decision_at"])
    parse_utc(document["input_available_at"])
    parse_utc(document["available_at"])
    assert isinstance(document["computation_delay_seconds"], int)
    assert document["computation_delay_seconds"] >= 0
    assert SHA256.fullmatch(document["input_fingerprint"])
    assert_timing(document)

    value_schema = schema["properties"]["features"]
    feature_properties = value_schema["properties"]
    assert set(document["features"]) == set(value_schema["required"])
    assert set(document["features"]) <= set(feature_properties)
    for value in document["features"].values():
        assert value is None or (isinstance(value, str) and DECIMAL.fullmatch(value))


def assert_manifest(document: dict, schema: dict) -> None:
    assert_fixed_metadata(document, schema, "feature manifest")
    assert document["feature_names"] == schema["properties"]["feature_names"]["const"]
    assert_artifact(document["artifact"], schema)
    for field in ("decision_at", "input_available_at", "available_at"):
        parse_utc(document[field])
    assert isinstance(document["computation_delay_seconds"], int)
    assert document["computation_delay_seconds"] >= 0
    assert SHA256.fullmatch(document["input_fingerprint"])
    assert_timing(document)

    for item in document["selected_input_manifests"]:
        assert set(item) == {"path", "sha256"}
        assert item["path"] and SHA256.fullmatch(item["sha256"])
    for item in document["selected_input_parts"]:
        assert set(item) == {"path", "sha256"}
        assert re.fullmatch(r"part-[0-9a-f]{64}\.parquet", item["path"])
        assert SHA256.fullmatch(item["sha256"])
    for item in document["parts"]:
        assert set(item) == {"path", "sha256", "row_count"}
        assert re.fullmatch(r"part-[0-9a-f]{64}\.parquet", item["path"])
        assert SHA256.fullmatch(item["sha256"])
        assert isinstance(item["row_count"], int) and item["row_count"] >= 0
    assert document["row_count"] == sum(item["row_count"] for item in document["parts"])
    assert document["input_fingerprint"] == input_fingerprint(document)


def expect_rejected(check, document: dict, label: str) -> None:
    try:
        check(document)
    except (AssertionError, KeyError):
        return
    raise AssertionError(f"{label} was accepted")


def main() -> int:
    observation_schema = load_json("feature-observation.schema.json")
    manifest_schema = load_json("feature-manifest.schema.json")
    observation = load_json("feature-observation.fixture.json")
    manifest = load_json("feature-manifest.fixture.json")

    assert_observation(observation, observation_schema)
    assert_manifest(manifest, manifest_schema)
    assert observation["artifact"] == manifest["artifact"]
    assert observation["input_fingerprint"] == manifest["input_fingerprint"]

    unknown_feature = json.loads(json.dumps(observation))
    unknown_feature["features"]["future_feature"] = "1.0"
    expect_rejected(
        lambda value: assert_observation(value, observation_schema),
        unknown_feature,
        "unknown feature",
    )

    unknown_version = json.loads(json.dumps(manifest))
    unknown_version["feature_set_version"] = "2.0.0"
    expect_rejected(
        lambda value: assert_manifest(value, manifest_schema),
        unknown_version,
        "unknown feature-set version",
    )

    numeric_value = json.loads(json.dumps(observation))
    numeric_value["features"]["close"] = 123.45
    expect_rejected(
        lambda value: assert_observation(value, observation_schema),
        numeric_value,
        "JSON numeric feature value",
    )

    wrong_availability = json.loads(json.dumps(observation))
    wrong_availability["available_at"] = "2026-08-12T14:29:31Z"
    expect_rejected(
        lambda value: assert_observation(value, observation_schema),
        wrong_availability,
        "incorrect derived availability",
    )

    print("validated feature fixtures and deterministic timing/fingerprint invariants")
    return 0


if __name__ == "__main__":
    sys.exit(main())
