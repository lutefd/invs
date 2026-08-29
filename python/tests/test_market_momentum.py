from __future__ import annotations

import hashlib
import json
from datetime import UTC, datetime, timedelta
from decimal import Decimal, localcontext
from pathlib import Path

import duckdb
import pytest

from research.catalog import ResearchCatalog
from research.feature_cli import main as feature_cli_main
from research.features import FeatureArtifactError, read_feature_artifact
from research.market_momentum import (
    FEATURE_NAMES,
    compute_market_momentum_features,
    publish_market_momentum,
    read_market_momentum_artifact,
)

SECURITY_ID = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
SOURCE_ID = "5d6ac836-54fd-4df2-a745-0744180420db"
RUN_ID = "c7286917-ce45-4879-834f-fc975c80c49e"
CALENDAR_PIN = {
    "data_source_id": "11111111-1111-4111-8111-111111111111",
    "mic": "XNAS",
    "calendar_version": "xnas_2025_fixture",
    "session_fingerprint": "f" * 64,
    "calendar_available_at": "2024-12-01T00:00:00Z",
    "decision_clock_policy": "after_close_next_session",
}


def _sql(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _price_select(row: dict[str, str], index: int) -> str:
    return f"""
        SELECT
          '1.0.0'::VARCHAR AS schema_version, 'yahoo'::VARCHAR AS source,
          '{SECURITY_ID}'::VARCHAR AS security_id, '1d'::VARCHAR AS interval,
          'raw'::VARCHAR AS price_basis, 'USD'::VARCHAR AS currency,
          TIMESTAMPTZ '{row['observed']}' AS observed_at,
          'second'::VARCHAR AS observed_precision,
          TIMESTAMPTZ '{row['observed']}' AS published_at, true AS has_published_at,
          'second'::VARCHAR AS published_precision,
          TIMESTAMPTZ '{row['available']}' AS available_at,
          TIMESTAMPTZ '{row['ingested']}' AS ingested_at,
          {_sql(row['open'])}::VARCHAR AS open,
          {_sql(row['high'])}::VARCHAR AS high,
          {_sql(row['low'])}::VARCHAR AS low,
          {_sql(row['close'])}::VARCHAR AS close,
          {_sql(row['volume'])}::VARCHAR AS volume, true AS has_volume,
          '{index:064x}'::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          'chart/result[{index}]'::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
    """


def _rows(count: int, *, final_close: int | None = None, zero_index: int | None = None) -> list[dict[str, str]]:
    start = datetime(2025, 1, 1, 21, tzinfo=UTC)
    rows: list[dict[str, str]] = []
    for index in range(count):
        close = 100 + index
        if final_close is not None and index == count - 1:
            close = final_close
        if zero_index is not None and index == zero_index:
            close = 0
        observed = start + timedelta(days=index)
        rows.append(
            {
                "observed": observed.strftime("%Y-%m-%dT%H:%M:%SZ"),
                "available": (observed + timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ"),
                "ingested": (observed + timedelta(hours=2)).strftime("%Y-%m-%dT%H:%M:%SZ"),
                "open": str(close),
                "high": str(close + 1),
                "low": str(max(0, close - 1)),
                "close": str(close),
                "volume": "100000",
            }
        )
    return rows


def _write_prices(data_root: Path, rows: list[dict[str, str]]) -> Path:
    directory = data_root / "normalized" / "prices" / "source=yahoo" / f"security_id={SECURITY_ID}"
    directory.mkdir(parents=True, exist_ok=True)
    temporary = directory / "prices.pending.parquet"
    query = "\nUNION ALL\n".join(_price_select(row, index) for index, row in enumerate(rows))
    connection = duckdb.connect(":memory:")
    connection.execute(f"COPY ({query}) TO '{temporary}' (FORMAT PARQUET)")
    connection.close()
    digest = hashlib.sha256(temporary.read_bytes()).hexdigest()
    part = directory / f"part-{digest}.parquet"
    temporary.rename(part)
    manifest = {
        "manifest_version": 1,
        "schema_version": "1.0.0",
        "normalizer_version": "go-v1",
        "git_commit": "0" * 40,
        "source": "yahoo",
        "data_source_id": SOURCE_ID,
        "ingestion_run_id": RUN_ID,
        "partition": {
            "dataset": "prices",
            "source": "yahoo",
            "security_id": SECURITY_ID,
        },
        "row_count": len(rows),
        "parts": [{"path": part.name, "sha256": digest, "row_count": len(rows)}],
    }
    (directory / "manifest.json").write_text(
        json.dumps(manifest, indent=2) + "\n",
        encoding="utf-8",
    )
    return directory / "manifest.json"


def _catalog(tmp_path: Path, rows: list[dict[str, str]]) -> ResearchCatalog:
    data_root = tmp_path / "data"
    _write_prices(data_root, rows)
    return ResearchCatalog(data_root).register()


def _decision_at(row: dict[str, str]) -> str:
    observed = datetime.fromisoformat(row["observed"])
    return (observed + timedelta(hours=2)).strftime("%Y-%m-%dT%H:%M:%SZ")


def _decimal_text(value: Decimal) -> str:
    return "0" if value.is_zero() else format(value, "f")


def _expected_features(closes: list[Decimal]) -> dict[str, str]:
    with localcontext() as context:
        context.prec = 256
        expected: dict[str, str] = {}
        for name, horizon in (
            ("return_1m", 21),
            ("return_3m", 63),
            ("return_6m", 126),
            ("return_12m", 252),
        ):
            expected[name] = _decimal_text(
                closes[-1] / closes[-(horizon + 1)] - Decimal(1)
            )
        trailing = closes[-22:]
        returns = [trailing[index] / trailing[index - 1] - Decimal(1) for index in range(1, 22)]
        mean = sum(returns, Decimal(0)) / Decimal(len(returns))
        variance = sum((value - mean) ** 2 for value in returns) / Decimal(len(returns) - 1)
        expected["realized_volatility_1m"] = _decimal_text(variance.sqrt() * Decimal(252).sqrt())
        peak = trailing[-21]
        drawdowns = []
        for close in trailing[-21:]:
            peak = max(peak, close)
            drawdowns.append(close / peak - Decimal(1))
        expected["max_drawdown_1m"] = _decimal_text(min(drawdowns))
        return expected


def test_market_momentum_publishes_exact_features_and_dispatches(tmp_path: Path) -> None:
    rows = _rows(253, final_close=300)
    catalog = _catalog(tmp_path, rows)
    decision_at = _decision_at(rows[-1])
    manifest_path = publish_market_momentum(
        catalog,
        decision_at=decision_at,
        security_id=SECURITY_ID,
        calendar_pin=CALENDAR_PIN,
        features_root=tmp_path / "features",
        computation_delay_seconds=30,
        git_commit="0" * 40,
    )

    artifact = read_feature_artifact(manifest_path)
    direct_artifact = read_market_momentum_artifact(manifest_path)
    manifest = artifact.manifest
    features = artifact.observations[0]["features"]
    closes = [Decimal(str(100 + index)) for index in range(252)] + [Decimal(300)]

    assert artifact.manifest == direct_artifact.manifest
    assert manifest["feature_set"] == "market-momentum"
    assert manifest["feature_names"] == list(FEATURE_NAMES)
    assert features == _expected_features(closes)
    assert manifest["input_available_at"] == rows[-1]["available"]
    assert manifest["available_at"] == "2025-09-10T22:00:30Z"
    assert manifest["row_count"] == 1


def test_market_momentum_warmup_respects_individual_prerequisites(tmp_path: Path) -> None:
    rows = _rows(64)
    catalog = _catalog(tmp_path, rows)

    at_21 = compute_market_momentum_features(
        catalog,
        decision_at=_decision_at(rows[20]),
        security_id=SECURITY_ID,
        calendar_pin=CALENDAR_PIN,
    )
    assert at_21["return_1m"] is None
    assert at_21["realized_volatility_1m"] is None
    assert at_21["max_drawdown_1m"] == "0"

    at_22 = compute_market_momentum_features(
        catalog,
        decision_at=_decision_at(rows[21]),
        security_id=SECURITY_ID,
        calendar_pin=CALENDAR_PIN,
    )
    assert at_22["return_1m"] is not None
    assert at_22["realized_volatility_1m"] is not None
    assert at_22["return_3m"] is None

    at_64 = compute_market_momentum_features(
        catalog,
        decision_at=_decision_at(rows[63]),
        security_id=SECURITY_ID,
        calendar_pin=CALENDAR_PIN,
    )
    assert at_64["return_3m"] is not None
    assert at_64["return_6m"] is None
    assert at_64["return_12m"] is None


def test_market_momentum_zero_denominator_fails_closed(tmp_path: Path) -> None:
    rows = _rows(22, zero_index=0)
    catalog = _catalog(tmp_path, rows)

    with pytest.raises(FeatureArtifactError, match="zero previous close"):
        publish_market_momentum(
            catalog,
            decision_at=_decision_at(rows[-1]),
            security_id=SECURITY_ID,
            calendar_pin=CALENDAR_PIN,
            features_root=tmp_path / "features",
        )


def test_market_momentum_publication_is_idempotent_and_tamper_evident(tmp_path: Path) -> None:
    rows = _rows(253)
    catalog = _catalog(tmp_path, rows)
    features_root = tmp_path / "features"
    kwargs = {
        "decision_at": _decision_at(rows[-1]),
        "security_id": SECURITY_ID,
        "calendar_pin": CALENDAR_PIN,
        "features_root": features_root,
        "git_commit": "0" * 40,
    }
    first = publish_market_momentum(catalog, **kwargs)
    first_bytes = first.read_bytes()
    first_mtime = first.stat().st_mtime_ns
    second = publish_market_momentum(catalog, **kwargs)
    assert second == first
    assert first.read_bytes() == first_bytes
    assert first.stat().st_mtime_ns == first_mtime

    part_path = read_market_momentum_artifact(first).part_paths[0]
    part_path.write_bytes(part_path.read_bytes() + b"tampered")
    with pytest.raises(FeatureArtifactError):
        read_feature_artifact(first)


def test_market_momentum_cli_dispatches_direct_publish(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    rows = _rows(22)
    catalog = _catalog(tmp_path, rows)
    calendar_path = tmp_path / "calendar.json"
    calendar_path.write_text(json.dumps(CALENDAR_PIN), encoding="utf-8")

    assert feature_cli_main(
        [
            "publish",
            "--data-root",
            str(catalog.data_root),
            "--features-root",
            str(tmp_path / "features"),
            "--security-id",
            SECURITY_ID,
            "--decision-at",
            _decision_at(rows[-1]),
            "--feature-set",
            "market-momentum",
            "--feature-set-version",
            "1.0.0",
            "--calendar-pin",
            str(calendar_path),
            "--git-commit",
            "0" * 40,
        ]
    ) == 0
    result = json.loads(capsys.readouterr().out)
    assert result["feature_set"] == "market-momentum"
    assert result["features"]["return_1m"] is not None
