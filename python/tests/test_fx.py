from __future__ import annotations

import hashlib
import json
from copy import deepcopy
from pathlib import Path

import duckdb
import pytest

from research import (
    FX_POLICY_VERSION,
    AmbiguousFXObservation,
    FXObservationNotFound,
    FXPolicyError,
    ResearchCatalog,
    convert_currency,
    fx_as_of,
    fx_record_hash,
)


def _observation() -> dict[str, object]:
    return {
        "schema_version": "1.0.0",
        "id": "4db8232a-0703-5d14-a020-cec2764cb347",
        "source": "bcb_ptax",
        "base_currency": "USD",
        "quote_currency": "BRL",
        "rate_kind": "ptax_closing",
        "fixing_timezone": "America/Sao_Paulo",
        "buy_rate": "5.223",
        "sell_rate": "5.2236",
        "fixing_at": "2026-08-14T16:10:22.94166Z",
        "published_at": "2026-08-14T16:10:22.94166Z",
        "available_at": "2026-08-14T16:10:22.94166Z",
        "recorded_at": "2026-08-14T17:00:00Z",
        "revision": 0,
        "source_record_id": "ptax-closing/USD-BRL/2026-08-14T13:10:22.94166-03:00",
        "raw_payload_hash": "a" * 64,
        "data_source_id": "5d6ac836-54fd-4df2-a745-0744180420db",
        "ingestion_run_id": "c7286917-ce45-4879-834f-fc975c80c49e",
        "raw_record_locator": "value/0",
        "normalizer_version": "bcb-ptax-v1",
    }


def test_fx_as_of_obeys_exact_bulletin_availability_and_never_carries() -> None:
    observation = _observation()
    with pytest.raises(FXObservationNotFound):
        fx_as_of(
            [observation],
            pair="USD-BRL",
            fixing_date="2026-08-14",
            decision_at="2026-08-14T16:10:22.941659Z",
        )
    assert (
        fx_as_of(
            [observation],
            pair="USD-BRL",
            fixing_date="2026-08-14",
            decision_at="2026-08-14T16:10:22.94166Z",
        )["id"]
        == observation["id"]
    )
    with pytest.raises(FXObservationNotFound):
        fx_as_of(
            [observation],
            pair="USD-BRL",
            fixing_date="2026-08-15",
            decision_at="2026-08-16T00:00:00Z",
        )


def test_fx_selection_rejects_unsupported_or_ambiguous_inputs() -> None:
    observation = _observation()
    with pytest.raises(FXPolicyError, match="unsupported FX pair"):
        fx_as_of(
            [observation],
            pair="EUR-BRL",
            fixing_date="2026-08-14",
            decision_at="2026-08-15T00:00:00Z",
        )
    with pytest.raises(AmbiguousFXObservation):
        fx_as_of(
            [observation, deepcopy(observation)],
            pair="USD-BRL",
            fixing_date="2026-08-14",
            decision_at="2026-08-15T00:00:00Z",
        )


def test_conversion_uses_sell_side_direct_and_inverse_with_complete_pin() -> None:
    observation = _observation()
    direct = convert_currency(
        "1", source_currency="USD", target_currency="BRL", observation=observation
    )
    assert direct["converted_amount"] == "5.2236"
    assert direct["policy_version"] == FX_POLICY_VERSION
    assert direct["fx_input"] == {
        "id": observation["id"],
        "source": "bcb_ptax",
        "base_currency": "USD",
        "quote_currency": "BRL",
        "rate_kind": "ptax_closing",
        "revision": 0,
        "fixing_at": "2026-08-14T16:10:22.94166Z",
        "available_at": "2026-08-14T16:10:22.94166Z",
        "sell_rate": "5.2236",
        "raw_payload_hash": "a" * 64,
        "ingestion_run_id": "c7286917-ce45-4879-834f-fc975c80c49e",
        "canonical_record_hash": fx_record_hash(observation),
        "policy_version": FX_POLICY_VERSION,
    }
    exact_inverse = convert_currency(
        "5.2236", source_currency="BRL", target_currency="USD", observation=observation
    )
    assert exact_inverse["converted_amount"] == "1"
    nonterminating = convert_currency(
        "1", source_currency="BRL", target_currency="USD", observation=observation
    )
    assert nonterminating["converted_amount"] == (
        "0.19143885442989509150777241748985374071521556015009"
    )


def test_conversion_identity_uses_no_fx_and_other_pairs_do_not_triangulate() -> None:
    identity = convert_currency("10", source_currency="USD", target_currency="USD")
    assert identity["converted_amount"] == "10"
    assert identity["fx_input"] is None
    with pytest.raises(FXPolicyError, match="triangulation is disabled"):
        convert_currency(
            "1", source_currency="EUR", target_currency="BRL", observation=_observation()
        )
    with pytest.raises(FXPolicyError, match="must not use"):
        convert_currency(
            "1", source_currency="USD", target_currency="USD", observation=_observation()
        )


def test_fx_record_hash_normalizes_equivalent_utc_timestamp_lexemes() -> None:
    original = _observation()
    equivalent = deepcopy(original)
    for field in ("fixing_at", "published_at", "available_at"):
        equivalent[field] = "2026-08-14T13:10:22.941660-03:00"
    assert fx_record_hash(equivalent) == fx_record_hash(original)


def _write_fx_manifest(root: Path) -> Path:
    observation = _observation()
    directory = root / "normalized" / "fx" / "source=bcb_ptax" / "pair=USD-BRL"
    directory.mkdir(parents=True)
    temporary = directory / "fx.parquet"
    connection = duckdb.connect(":memory:")
    connection.execute(
        f"""
        COPY (SELECT
          '1.0.0'::VARCHAR AS schema_version,
          '{observation['id']}'::VARCHAR AS id,
          'bcb_ptax'::VARCHAR AS source,
          'USD'::VARCHAR AS base_currency,
          'BRL'::VARCHAR AS quote_currency,
          'ptax_closing'::VARCHAR AS rate_kind,
          'America/Sao_Paulo'::VARCHAR AS fixing_timezone,
          '5.223'::VARCHAR AS buy_rate,
          '5.2236'::VARCHAR AS sell_rate,
          TIMESTAMPTZ '2026-08-14T16:10:22.94166Z' AS fixing_at,
          TIMESTAMPTZ '2026-08-14T16:10:22.94166Z' AS published_at,
          TIMESTAMPTZ '2026-08-14T16:10:22.94166Z' AS available_at,
          TIMESTAMPTZ '2026-08-14T17:00:00Z' AS recorded_at,
          0::INTEGER AS revision,
          '{observation['source_record_id']}'::VARCHAR AS source_record_id,
          repeat('a', 64)::VARCHAR AS raw_payload_hash,
          '{observation['data_source_id']}'::VARCHAR AS data_source_id,
          '{observation['ingestion_run_id']}'::VARCHAR AS ingestion_run_id,
          'value/0'::VARCHAR AS raw_record_locator,
          'bcb-ptax-v1'::VARCHAR AS normalizer_version
        ) TO '{temporary}' (FORMAT PARQUET)
        """
    )
    connection.close()
    digest = hashlib.sha256(temporary.read_bytes()).hexdigest()
    part = directory / f"part-{digest}.parquet"
    temporary.replace(part)
    manifest = directory / "manifest.json"
    manifest.write_text(
        json.dumps(
            {
                "manifest_version": 1,
                "schema_version": "1.0.0",
                "normalizer_version": "bcb-ptax-v1",
                "git_commit": "0" * 40,
                "source": "bcb_ptax",
                "data_source_id": observation["data_source_id"],
                "ingestion_run_id": observation["ingestion_run_id"],
                "partition": {
                    "dataset": "fx",
                    "source": "bcb_ptax",
                    "pair": "USD-BRL",
                },
                "row_count": 1,
                "parts": [{"path": part.name, "sha256": digest, "row_count": 1}],
            }
        ),
        encoding="utf-8",
    )
    return manifest


def test_catalog_registers_verified_fx_and_resolves_exact_fixing(tmp_path: Path) -> None:
    manifest = _write_fx_manifest(tmp_path)
    catalog = ResearchCatalog(tmp_path).register()
    status = {item.name: item for item in catalog.status()}["fx"]
    assert status.available and status.row_count == 1
    selected = catalog.fx_as_of(
        pair="USD-BRL",
        fixing_date="2026-08-14",
        decision_at="2026-08-14T16:10:22.94166Z",
    )
    assert selected["sell_rate"] == "5.2236"
    assert selected["manifest_path"] == str(manifest.resolve())
    assert Path(selected["part_path"]).name == f"part-{selected['part_sha256']}.parquet"
