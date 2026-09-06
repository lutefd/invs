"""Deterministic cross-sectional stock discovery indexes."""

from __future__ import annotations

import hashlib
import json
from collections.abc import Mapping
from datetime import date, datetime
from decimal import ROUND_HALF_EVEN, Decimal
from pathlib import Path
from typing import Any
from uuid import UUID, uuid5

import yaml

from .batches import ValidatedFeatureBatch, validate_feature_batch
from .experiments import canonical_json
from .registry import load_feature_registry

SCHEMA_VERSION = "1.0.0"
MODEL_NAME = "momentum-risk-rank"
MODEL_VERSION = "1.0.0"
_NAMESPACE = UUID("e0a6252a-147c-5a50-88d9-acd02c49095e")
_WEIGHTS = {
    "return_3m": Decimal("0.25"),
    "return_6m": Decimal("0.30"),
    "return_12m": Decimal("0.30"),
    "low_volatility_1m": Decimal("0.10"),
    "shallow_drawdown_1m": Decimal("0.05"),
}
_REQUIRED_FEATURES = (
    "return_1m",
    "return_3m",
    "return_6m",
    "return_12m",
    "realized_volatility_1m",
    "max_drawdown_1m",
)


class DiscoveryError(ValueError):
    """Raised when a discovery index cannot be safely published or read."""


def _load_profile(path: str | Path) -> dict[str, Any]:
    document = yaml.safe_load(Path(path).read_text(encoding="utf-8"))
    if not isinstance(document, dict):
        raise DiscoveryError("universe profile must be an object")
    required = {"schema_version", "universe_id", "name", "membership_as_of", "members"}
    if not required <= set(document):
        raise DiscoveryError("universe profile is missing required fields")
    UUID(str(document["universe_id"]))
    date.fromisoformat(str(document["membership_as_of"]))
    members = document["members"]
    if not isinstance(members, list) or not members:
        raise DiscoveryError("universe profile members must be a non-empty array")
    security_ids: set[str] = set()
    tickers: set[str] = set()
    for member in members:
        if not isinstance(member, dict):
            raise DiscoveryError("universe member must be an object")
        security_id = str(UUID(str(member["security_id"])))
        ticker = str(member["ticker"]).strip().upper()
        if not ticker or security_id in security_ids or ticker in tickers:
            raise DiscoveryError("universe profile contains an invalid or duplicate member")
        security_ids.add(security_id)
        tickers.add(ticker)
    return document


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _fmt(value: Decimal, places: str = "0.000000000001") -> str:
    rendered = format(value.quantize(Decimal(places), rounding=ROUND_HALF_EVEN), "f")
    return rendered.rstrip("0").rstrip(".") if "." in rendered else rendered


def _percentiles(values: Mapping[str, Decimal], *, higher_is_better: bool = True) -> dict[str, Decimal]:
    if len(values) == 1:
        return {key: Decimal("0.5") for key in values}
    result: dict[str, Decimal] = {}
    denominator = Decimal(len(values) - 1)
    for key, value in values.items():
        better_direction = sum(
            Decimal(1)
            for other in values.values()
            if (other < value if higher_is_better else other > value)
        )
        ties = sum(1 for other in values.values() if other == value)
        result[key] = (better_direction + Decimal(ties - 1) / 2) / denominator
    return result


def _validated_batch(
    manifest_path: str | Path, *, features_root: str | Path, registry_path: str | Path
) -> ValidatedFeatureBatch:
    registry = load_feature_registry(registry_path)
    batch = validate_feature_batch(manifest_path, features_root=features_root, registry=registry)
    if batch.manifest["feature_set"] != "market-momentum" or batch.manifest["feature_set_version"] != "1.0.0":
        raise DiscoveryError("discovery requires market-momentum 1.0.0")
    if len(batch.manifest["decision_schedule"]) != 1:
        raise DiscoveryError("discovery requires exactly one decision timestamp")
    return batch


def _existing_for_session(output_root: Path, market_session: str) -> Path | None:
    matches = sorted((output_root / "indexes").glob(f"{market_session}-*/manifest.json"))
    if len(matches) > 1:
        raise DiscoveryError(f"multiple discovery indexes exist for market session {market_session}")
    return matches[0] if matches else None


def publish_discovery_index(
    *,
    profile_path: str | Path,
    momentum_batch_manifest: str | Path,
    features_root: str | Path,
    registry_path: str | Path,
    output_root: str | Path,
    market_session: str,
    candidate_count: int = 5,
    git_commit: str = "unknown",
) -> Path:
    """Rank a validated momentum batch and publish one immutable daily index."""

    profile = _load_profile(profile_path)
    session = date.fromisoformat(market_session).isoformat()
    output = Path(output_root).resolve()
    existing = _existing_for_session(output, session)
    if existing is not None:
        read_discovery_index(existing)
        return existing
    batch = _validated_batch(
        momentum_batch_manifest, features_root=features_root, registry_path=registry_path
    )
    decision_at = batch.manifest["decision_schedule"][0]
    if date.fromisoformat(str(profile["membership_as_of"])) > datetime.fromisoformat(decision_at).date():
        raise DiscoveryError("universe membership was not admitted by the decision timestamp")
    members = {str(member["security_id"]): member for member in profile["members"]}
    declared = set(batch.manifest["universe"]["security_ids"])
    if declared != set(members):
        raise DiscoveryError("momentum batch universe differs from the discovery profile")
    if candidate_count <= 0 or candidate_count > len(members):
        raise DiscoveryError("candidate_count must be within the universe size")

    observed: dict[str, dict[str, Any]] = {}
    for item in batch.observations:
        security_id = item["security_id"]
        if security_id in observed:
            raise DiscoveryError(f"duplicate momentum observation for {security_id}")
        observed[security_id] = item
    rejected = {item["security_id"]: item for item in batch.manifest["rejected"]}
    values: dict[str, dict[str, Decimal]] = {}
    rejection_reasons: dict[str, list[str]] = {}
    for security_id in sorted(members):
        item = observed.get(security_id)
        if item is None:
            reason = rejected.get(security_id, {}).get("reason", "missing_momentum_observation")
            rejection_reasons[security_id] = [str(reason)]
            continue
        features = item["features"]
        missing = [name for name in _REQUIRED_FEATURES if features.get(name) is None]
        if missing:
            rejection_reasons[security_id] = [f"null_feature:{name}" for name in missing]
            continue
        values[security_id] = {name: Decimal(str(features[name])) for name in _REQUIRED_FEATURES}
    if not values:
        raise DiscoveryError("no securities have complete momentum features")

    components = {
        "return_3m": _percentiles({key: row["return_3m"] for key, row in values.items()}),
        "return_6m": _percentiles({key: row["return_6m"] for key, row in values.items()}),
        "return_12m": _percentiles({key: row["return_12m"] for key, row in values.items()}),
        "low_volatility_1m": _percentiles(
            {key: row["realized_volatility_1m"] for key, row in values.items()},
            higher_is_better=False,
        ),
        "shallow_drawdown_1m": _percentiles(
            {key: row["max_drawdown_1m"] for key, row in values.items()}
        ),
    }
    scores = {
        security_id: sum(
            (components[name][security_id] * weight for name, weight in _WEIGHTS.items()),
            Decimal(0),
        )
        * 100
        for security_id in values
    }
    ordered = sorted(values, key=lambda item: (-scores[item], members[item]["ticker"], item))
    rows: list[dict[str, Any]] = []
    for rank, security_id in enumerate(ordered, start=1):
        member = members[security_id]
        rows.append(
            {
                "rank": rank,
                "security_id": security_id,
                "ticker": member["ticker"],
                "sector": member["sector"],
                "candidate": rank <= candidate_count,
                "score": _fmt(scores[security_id], "0.000001"),
                "features": {name: _fmt(values[security_id][name]) for name in _REQUIRED_FEATURES},
                "component_percentiles": {
                    name: _fmt(component[security_id], "0.000001")
                    for name, component in components.items()
                },
                "eligibility": {"status": "eligible", "reasons": []},
            }
        )
    for security_id in sorted(rejection_reasons, key=lambda item: members[item]["ticker"]):
        member = members[security_id]
        rows.append(
            {
                "rank": None,
                "security_id": security_id,
                "ticker": member["ticker"],
                "sector": member["sector"],
                "candidate": False,
                "score": None,
                "features": {name: None for name in _REQUIRED_FEATURES},
                "component_percentiles": {name: None for name in _WEIGHTS},
                "eligibility": {"status": "rejected", "reasons": rejection_reasons[security_id]},
            }
        )

    batch_path = Path(momentum_batch_manifest).resolve()
    feature_root = Path(features_root).resolve()
    try:
        relative_batch = batch_path.relative_to(feature_root).as_posix()
    except ValueError as error:
        raise DiscoveryError("momentum batch must be below features_root") from error
    identity = f"{MODEL_NAME}:{MODEL_VERSION}:{profile['universe_id']}:{session}"
    discovery_id = str(uuid5(_NAMESPACE, identity))
    document = {
        "schema_version": SCHEMA_VERSION,
        "discovery_id": discovery_id,
        "model": {
            "name": MODEL_NAME,
            "version": MODEL_VERSION,
            "git_commit": git_commit,
            "weights": {name: _fmt(weight) for name, weight in _WEIGHTS.items()},
        },
        "universe": {
            "universe_id": str(profile["universe_id"]),
            "name": profile["name"],
            "membership_as_of": str(profile["membership_as_of"]),
            "fingerprint": batch.manifest["universe"]["fingerprint"],
            "size": len(members),
        },
        "market_session": session,
        "decision_at": decision_at,
        "feature_batch": {
            "batch_id": batch.manifest["batch"]["batch_id"],
            "feature_set": "market-momentum",
            "feature_set_version": "1.0.0",
            "input_fingerprint": batch.manifest["input_fingerprint"],
            "manifest_path": relative_batch,
            "manifest_sha256": _sha256(batch_path),
            "input_fitness": batch.manifest["input_fitness"],
        },
        "policy": {
            "candidate_count": min(candidate_count, len(values)),
            "purpose": "research_watchlist_only",
            "automatic_trading": False,
        },
        "summary": {
            "eligible_count": len(values),
            "rejected_count": len(rejection_reasons),
            "candidate_count": min(candidate_count, len(values)),
        },
        "rows": rows,
    }
    directory = output / "indexes" / f"{session}-{discovery_id}"
    directory.mkdir(parents=True, exist_ok=False)
    manifest_path = directory / "manifest.json"
    manifest_path.write_bytes(canonical_json(document) + b"\n")
    return manifest_path


def read_discovery_index(path: str | Path) -> dict[str, Any]:
    """Read and perform strict structural validation of one discovery index."""

    manifest_path = Path(path).resolve()
    document = json.loads(manifest_path.read_text(encoding="utf-8"))
    expected = {
        "schema_version",
        "discovery_id",
        "model",
        "universe",
        "market_session",
        "decision_at",
        "feature_batch",
        "policy",
        "summary",
        "rows",
    }
    if not isinstance(document, dict) or set(document) != expected:
        raise DiscoveryError("discovery manifest fields are invalid")
    if document["schema_version"] != SCHEMA_VERSION:
        raise DiscoveryError("unsupported discovery schema version")
    UUID(document["discovery_id"])
    date.fromisoformat(document["market_session"])
    model = document["model"]
    if not isinstance(model, dict) or set(model) != {"name", "version", "git_commit", "weights"}:
        raise DiscoveryError("discovery model fields are invalid")
    expected_weights = {name: _fmt(weight) for name, weight in _WEIGHTS.items()}
    if model["name"] != MODEL_NAME or model["version"] != MODEL_VERSION or model["weights"] != expected_weights:
        raise DiscoveryError("discovery model contract is unsupported")
    universe = document["universe"]
    if not isinstance(universe, dict) or set(universe) != {
        "universe_id", "name", "membership_as_of", "fingerprint", "size"
    }:
        raise DiscoveryError("discovery universe fields are invalid")
    UUID(universe["universe_id"])
    date.fromisoformat(universe["membership_as_of"])
    feature_batch = document["feature_batch"]
    if not isinstance(feature_batch, dict) or set(feature_batch) != {
        "batch_id", "feature_set", "feature_set_version", "input_fingerprint",
        "manifest_path", "manifest_sha256", "input_fitness"
    }:
        raise DiscoveryError("discovery feature batch fields are invalid")
    if feature_batch["feature_set"] != "market-momentum" or feature_batch["feature_set_version"] != "1.0.0":
        raise DiscoveryError("discovery feature batch contract is unsupported")
    policy = document["policy"]
    if not isinstance(policy, dict) or not isinstance(policy.get("candidate_count"), int) or policy != {
        "candidate_count": policy.get("candidate_count"),
        "purpose": "research_watchlist_only",
        "automatic_trading": False,
    }:
        raise DiscoveryError("discovery policy is invalid")
    summary = document["summary"]
    if not isinstance(summary, dict) or set(summary) != {
        "eligible_count", "rejected_count", "candidate_count"
    } or not all(isinstance(summary[item], int) for item in summary):
        raise DiscoveryError("discovery summary fields are invalid")
    if not isinstance(document["rows"], list) or len(document["rows"]) != universe["size"]:
        raise DiscoveryError("discovery rows differ from universe size")
    row_fields = {
        "rank", "security_id", "ticker", "sector", "candidate", "score", "features",
        "component_percentiles", "eligibility"
    }
    feature_fields = set(_REQUIRED_FEATURES)
    component_fields = set(_WEIGHTS)
    seen_security: set[str] = set()
    for row in document["rows"]:
        if not isinstance(row, dict) or set(row) != row_fields:
            raise DiscoveryError("discovery row fields are invalid")
        security_id = str(UUID(row["security_id"]))
        if security_id in seen_security:
            raise DiscoveryError("discovery rows contain a duplicate security")
        seen_security.add(security_id)
        if set(row["features"]) != feature_fields or set(row["component_percentiles"]) != component_fields:
            raise DiscoveryError("discovery row feature fields are invalid")
        eligibility = row["eligibility"]
        if not isinstance(eligibility, dict) or set(eligibility) != {"status", "reasons"}:
            raise DiscoveryError("discovery row eligibility fields are invalid")
        if eligibility["status"] == "eligible":
            if row["rank"] is None or row["score"] is None:
                raise DiscoveryError("eligible discovery row is missing rank or score")
        elif eligibility["status"] == "rejected":
            if row["rank"] is not None or row["score"] is not None or row["candidate"]:
                raise DiscoveryError("rejected discovery row has a rank, score, or candidate flag")
        else:
            raise DiscoveryError("discovery row eligibility is unsupported")
    ranks = [row["rank"] for row in document["rows"] if row["rank"] is not None]
    if ranks != list(range(1, len(ranks) + 1)):
        raise DiscoveryError("discovery ranks are not contiguous")
    if summary["eligible_count"] != len(ranks):
        raise DiscoveryError("discovery eligible count is inconsistent")
    if sum(bool(row["candidate"]) for row in document["rows"]) != summary["candidate_count"]:
        raise DiscoveryError("discovery candidate count is inconsistent")
    return document


__all__ = [
    "MODEL_NAME",
    "MODEL_VERSION",
    "DiscoveryError",
    "publish_discovery_index",
    "read_discovery_index",
]
