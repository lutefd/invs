from __future__ import annotations

import hashlib
import json
from pathlib import Path

import duckdb
import pytest

from research import DatasetSchemaError, ResearchCatalog, SecurityMapping, load_security_mappings

SECURITY_ID = "469fc20f-7d4b-45bb-b827-05f8410e71aa"
ISSUER_ID = "1b3d88f5-55b8-4dc5-a6be-2f77e9e99201"
SOURCE_ID = "5d6ac836-54fd-4df2-a745-0744180420db"
RUN_ID = "c7286917-ce45-4879-834f-fc975c80c49e"


def _write_parquet(path: Path, query: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    connection = duckdb.connect(":memory:")
    connection.execute(f"COPY ({query}) TO '{path}' (FORMAT PARQUET)")
    connection.close()


def _write_manifest(
    manifest_path: Path,
    part_paths: list[Path],
    *,
    dataset: str,
    source: str,
    partition_key: str,
    partition_value: str,
) -> Path:
    parts = []
    for part_path in part_paths:
        digest = hashlib.sha256(part_path.read_bytes()).hexdigest()
        committed_path = part_path.with_name(f"part-{digest}.parquet")
        if part_path != committed_path:
            part_path.replace(committed_path)
        connection = duckdb.connect(":memory:")
        row_count = connection.execute(
            f"SELECT count(*) FROM read_parquet('{committed_path}')"
        ).fetchone()[0]
        connection.close()
        parts.append(
            {
                "path": committed_path.name,
                "sha256": digest,
                "row_count": row_count,
            }
        )
    manifest_path.parent.mkdir(parents=True, exist_ok=True)
    manifest_path.write_text(
        json.dumps(
            {
                "manifest_version": 1,
                "schema_version": "1.0.0",
                "normalizer_version": "go-v1",
                "git_commit": "0" * 40,
                "source": source,
                "data_source_id": SOURCE_ID,
                "ingestion_run_id": RUN_ID,
                "partition": {
                    "dataset": dataset,
                    "source": source,
                    partition_key: partition_value,
                },
                "row_count": sum(part["row_count"] for part in parts),
                "parts": parts,
            }
        ),
        encoding="utf-8",
    )
    return manifest_path


def _price_select(
    *,
    observed: str = "2025-01-10 21:00:00Z",
    available: str = "2025-01-11 00:00:00Z",
    close: str = "100.123456789012345678",
    schema_version: str = "1.0.0",
    observed_precision: str | None = None,
) -> str:
    observed_precision_column = (
        f"'{observed_precision}'::VARCHAR AS observed_precision,"
        if observed_precision is not None
        else ""
    )
    return f"""
        SELECT
          '{schema_version}'::VARCHAR AS schema_version, 'yahoo'::VARCHAR AS source,
          '{SECURITY_ID}'::VARCHAR AS security_id, '1d'::VARCHAR AS interval,
          'raw'::VARCHAR AS price_basis, 'USD'::VARCHAR AS currency,
          TIMESTAMPTZ '{observed}' AS observed_at,
          {observed_precision_column}
          TIMESTAMPTZ '{observed}' AS published_at, true AS has_published_at,
          'second'::VARCHAR AS published_precision,
          TIMESTAMPTZ '{available}' AS available_at,
          TIMESTAMPTZ '{available}' AS ingested_at,
          '99.5'::VARCHAR AS open, '101.5'::VARCHAR AS high,
          '98.25'::VARCHAR AS low, '{close}'::VARCHAR AS close,
          '1000.25'::VARCHAR AS volume, true AS has_volume,
          repeat('a', 64)::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          'chart/result[0]'::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
    """


def _write_price_parts(root: Path, queries: list[str]) -> Path:
    directory = root / "prices" / "source=yahoo" / f"security_id={SECURITY_ID}"
    paths = []
    for index, query in enumerate(queries):
        path = directory / f"prices-{index}.parquet"
        _write_parquet(path, query)
        paths.append(path)
    return _write_manifest(
        directory / "manifest.json",
        paths,
        dataset="prices",
        source="yahoo",
        partition_key="security_id",
        partition_value=SECURITY_ID,
    )


def _write_prices(root: Path) -> Path:
    directory = root / "prices" / "source=yahoo" / f"security_id={SECURITY_ID}"
    path = directory / "prices.parquet"
    _write_parquet(
        path,
        f"""
        {_price_select()}
        UNION ALL
        {_price_select(observed="2025-02-10 21:00:00Z", available="2025-02-11 00:00:00Z", close="110")}
        UNION ALL
        {_price_select(observed="2026-01-10 21:00:00Z", available="2025-02-01 00:00:00Z", close="200")}
        """,
    )
    return _write_manifest(
        directory / "manifest.json",
        [path],
        dataset="prices",
        source="yahoo",
        partition_key="security_id",
        partition_value=SECURITY_ID,
    )


def _fundamental_select(
    *, sentinel: bool = False, observed_precision: str | None = None
) -> str:
    period_start = "DATE '1970-01-01'" if sentinel else "DATE '2024-10-01'"
    has_period_start = "false" if sentinel else "true"
    observed_precision_column = (
        f"'{observed_precision}'::VARCHAR AS observed_precision,"
        if observed_precision is not None
        else ""
    )
    return f"""
        SELECT
          '1.0.0'::VARCHAR AS schema_version, 'sec'::VARCHAR AS source,
          '{ISSUER_ID}'::VARCHAR AS issuer_id, ''::VARCHAR AS security_id,
          false AS has_security_id, 'us-gaap'::VARCHAR AS taxonomy,
          'Revenue'::VARCHAR AS concept, 'USD'::VARCHAR AS unit,
          'USD'::VARCHAR AS currency, true AS has_currency,
          TIMESTAMPTZ '2024-12-31 00:00:00Z' AS observed_at,
          {observed_precision_column}
          TIMESTAMPTZ '2025-01-20 12:00:00Z' AS published_at,
          'second'::VARCHAR AS published_precision,
          TIMESTAMPTZ '2025-01-20 12:00:00Z' AS available_at,
          TIMESTAMPTZ '2025-03-01 00:00:00Z' AS ingested_at,
          {period_start} AS period_start, {has_period_start} AS has_period_start,
          DATE '2024-12-31' AS period_end, '11.000000000000000001'::VARCHAR AS value,
          true AS has_value, 0::INTEGER AS revision, '0002'::VARCHAR AS accession_number,
          '10-Q'::VARCHAR AS form, 2024::INTEGER AS fiscal_year,
          'Q4'::VARCHAR AS fiscal_period, ''::VARCHAR AS frame,
          repeat('b', 64)::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          'companyfacts/facts/0'::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
        """


def _write_fundamental_parts(root: Path, queries: list[str]) -> Path:
    directory = root / "fundamentals" / "source=sec" / f"issuer_id={ISSUER_ID}"
    paths = []
    for index, query in enumerate(queries):
        path = directory / f"fundamentals-{index}.parquet"
        _write_parquet(path, query)
        paths.append(path)
    return _write_manifest(
        directory / "manifest.json",
        paths,
        dataset="fundamentals",
        source="sec",
        partition_key="issuer_id",
        partition_value=ISSUER_ID,
    )


def _write_fundamentals(
    root: Path, *, sentinel: bool = False, observed_precision: str | None = None
) -> Path:
    return _write_fundamental_parts(
        root,
        [_fundamental_select(sentinel=sentinel, observed_precision=observed_precision)],
    )


def _macro_select(
    *,
    observed: str = "2025-01-01 00:00:00Z",
    published: str = "2025-01-15 13:00:00Z",
    available: str = "2025-01-15 13:00:00Z",
    ingested: str = "2025-03-01 00:00:00Z",
    value: str = "2.100000000000000001",
    revision: int = 0,
    raw_hash: str = "c" * 64,
    vintage_at: str = "2025-01-15 13:00:00Z",
    has_vintage: bool = True,
    observed_precision: str | None = None,
) -> str:
    physical_vintage = (
        f"TIMESTAMPTZ '{vintage_at}'"
        if has_vintage
        else "TIMESTAMPTZ '1970-01-01 00:00:00Z'"
    )
    observed_precision_column = (
        f"'{observed_precision}'::VARCHAR AS observed_precision,"
        if observed_precision is not None
        else ""
    )
    return f"""
        SELECT
          '1.0.0'::VARCHAR AS schema_version, 'fred'::VARCHAR AS source,
          'GDP'::VARCHAR AS series_id, 'US'::VARCHAR AS geography,
          'Index'::VARCHAR AS unit, 'quarterly'::VARCHAR AS frequency,
          ''::VARCHAR AS seasonal_adjustment, false AS has_seasonal_adjustment,
          TIMESTAMPTZ '{observed}' AS observed_at,
          {observed_precision_column}
          TIMESTAMPTZ '{published}' AS published_at,
          'second'::VARCHAR AS published_precision,
          TIMESTAMPTZ '{available}' AS available_at,
          TIMESTAMPTZ '{ingested}' AS ingested_at,
          '{value}'::VARCHAR AS value, true AS has_value,
          {revision}::INTEGER AS revision, {physical_vintage} AS vintage_at,
          {'true' if has_vintage else 'false'} AS has_vintage_at,
          '{raw_hash}'::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          'csv/date=2025-01-01'::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
    """


def _write_macro_parts(root: Path, queries: list[str]) -> Path:
    directory = root / "macroeconomics" / "source=fred" / "series_id=GDP"
    paths = []
    for index, query in enumerate(queries):
        path = directory / f"macroeconomics-{index}.parquet"
        _write_parquet(path, query)
        paths.append(path)
    return _write_manifest(
        directory / "manifest.json",
        paths,
        dataset="macroeconomics",
        source="fred",
        partition_key="series_id",
        partition_value="GDP",
    )


def _write_macro_rows(root: Path, queries: list[str]) -> Path:
    return _write_macro_parts(root, ["\nUNION ALL\n".join(queries)])


def _write_macro(root: Path, *, sentinel: bool = False) -> Path:
    return _write_macro_rows(
        root,
        [_macro_select(has_vintage=not sentinel)],
    )


def _sql_string(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _filing_select(
    *,
    filing_id: str = "3b3d88f5-55b8-4dc5-a6be-2f77e9e99201",
    source: str = "cvm",
    source_document_id: str = "cvm-ipe:1023:12345:v1",
    form_type: str = "cvm_ipe",
    accession_number: str = "12345",
    category: str = "Categoria 'exata'; ação",
    document_type: str = "Tipo exato",
    species: str = "Espécie exata",
    subject: str = "Assunto exato",
    presentation_type: str = "Tipo_Apresentacao exato",
    primary_document: str = "not-downloaded",
    amends_source_document_id: str = "",
    filing_date: str = "2026-01-07",
    period_end: str = "2020-12-31",
    has_period_end: bool = True,
    observed: str = "2020-12-31 00:00:00Z",
    has_observed: bool = True,
    observed_precision: str = "date",
    published: str = "1970-01-01 00:00:00Z",
    has_published: bool = False,
    published_precision: str = "unknown",
    available: str = "2026-01-08 00:00:00Z",
    effective: str = "1970-01-01 00:00:00Z",
    has_effective: bool = False,
    ingested: str = "2026-01-08 00:00:00Z",
    raw_hash: str = "d" * 64,
    locator: str = "zip=2026/member=ipe.csv/row=7",
) -> str:
    period_sql = f"DATE {_sql_string(period_end if has_period_end else '1970-01-01')}"
    observed_sql = f"TIMESTAMPTZ {_sql_string(observed if has_observed else '1970-01-01 00:00:00Z')}"
    published_sql = f"TIMESTAMPTZ {_sql_string(published if has_published else '1970-01-01 00:00:00Z')}"
    effective_sql = f"TIMESTAMPTZ {_sql_string(effective if has_effective else '1970-01-01 00:00:00Z')}"
    return f"""
        SELECT
          '1.0.0'::VARCHAR AS schema_version,
          {_sql_string(filing_id)}::VARCHAR AS id,
          {_sql_string(source)}::VARCHAR AS source,
          '{ISSUER_ID}'::VARCHAR AS issuer_id,
          {_sql_string(source_document_id)}::VARCHAR AS source_document_id,
          'https://dados.cvm.gov.br/dados/CIA_ABERTA/DOC/IPE/DADOS/ipe_2026.zip'::VARCHAR AS document_url,
          {_sql_string(accession_number)}::VARCHAR AS accession_number,
          {_sql_string(form_type)}::VARCHAR AS form_type,
          {_sql_string(category)}::VARCHAR AS category,
          {_sql_string(document_type)}::VARCHAR AS document_type,
          {_sql_string(species)}::VARCHAR AS species,
          {_sql_string(subject)}::VARCHAR AS subject,
          {_sql_string(presentation_type)}::VARCHAR AS presentation_type,
          {_sql_string(primary_document)}::VARCHAR AS primary_document,
          {_sql_string(amends_source_document_id)}::VARCHAR AS amends_source_document_id,
          DATE {_sql_string(filing_date)} AS filing_date,
          {period_sql} AS period_end, {'true' if has_period_end else 'false'} AS has_period_end,
          {observed_sql} AS observed_at, {'true' if has_observed else 'false'} AS has_observed_at,
          {_sql_string(observed_precision)}::VARCHAR AS observed_precision,
          {published_sql} AS published_at, {'true' if has_published else 'false'} AS has_published_at,
          {_sql_string(published_precision)}::VARCHAR AS published_precision,
          TIMESTAMPTZ {_sql_string(available)} AS available_at,
          {effective_sql} AS effective_at, {'true' if has_effective else 'false'} AS has_effective_at,
          TIMESTAMPTZ {_sql_string(ingested)} AS ingested_at,
          {_sql_string(raw_hash)}::VARCHAR AS raw_payload_hash,
          '{SOURCE_ID}'::VARCHAR AS data_source_id,
          '{RUN_ID}'::VARCHAR AS ingestion_run_id,
          {_sql_string(locator)}::VARCHAR AS raw_record_locator,
          'go-v1'::VARCHAR AS normalizer_version
    """


def _write_filing_parts(root: Path, queries: list[str]) -> Path:
    directory = root / "filings" / "source=cvm" / f"issuer_id={ISSUER_ID}"
    paths = []
    for index, query in enumerate(queries):
        path = directory / f"filings-{index}.parquet"
        _write_parquet(path, query)
        paths.append(path)
    return _write_manifest(
        directory / "manifest.json",
        paths,
        dataset="filings",
        source="cvm",
        partition_key="issuer_id",
        partition_value=ISSUER_ID,
    )


def test_fresh_data_root_registers_typed_empty_views(tmp_path: Path) -> None:
    catalog = ResearchCatalog(tmp_path).register()
    mapping = SecurityMapping("missing", "missing")

    assert catalog.missing() == ("prices", "fundamentals", "macroeconomics", "filings")
    assert catalog.connection.execute("select count(*) from prices_canonical").fetchone() == (0,)
    assert catalog.connection.execute("select count(*) from filings_canonical").fetchone() == (0,)
    assert catalog.research_snapshot(
        decision_at="2025-03-01T00:00:00Z",
        mapping=mapping,
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    ).empty


def test_valid_manifest_registers_only_its_verified_parts(tmp_path: Path) -> None:
    root = tmp_path / "normalized"
    manifest_path = _write_prices(root)
    _write_parquet(
        manifest_path.parent / "stray.parquet",
        _price_select(close="999"),
    )

    catalog = ResearchCatalog(tmp_path).register()

    assert catalog.connection.execute("SELECT count(*) FROM prices_canonical").fetchone() == (3,)
    assert catalog.status()[0].file_count == 1


def test_legacy_data_parquet_is_ignored_without_a_manifest(tmp_path: Path) -> None:
    legacy = (
        tmp_path
        / "normalized"
        / "prices"
        / "source=yahoo"
        / f"security_id={SECURITY_ID}"
        / "data.parquet"
    )
    _write_parquet(legacy, _price_select())

    catalog = ResearchCatalog(tmp_path).register()

    assert catalog.missing() == ("prices", "fundamentals", "macroeconomics", "filings")
    assert catalog.connection.execute("SELECT count(*) FROM prices_canonical").fetchone() == (0,)


@pytest.mark.parametrize("mutation", ["missing", "tampered"])
def test_missing_or_tampered_manifest_part_is_rejected(
    tmp_path: Path, mutation: str
) -> None:
    manifest_path = _write_prices(tmp_path / "normalized")
    part_path = manifest_path.parent / json.loads(
        manifest_path.read_text(encoding="utf-8")
    )["parts"][0]["path"]
    if mutation == "missing":
        part_path.unlink()
        expected = "does not exist"
    else:
        part_path.write_bytes(part_path.read_bytes() + b"tampered")
        expected = "SHA-256 mismatch"

    with pytest.raises(DatasetSchemaError, match=expected):
        ResearchCatalog(tmp_path).register()


def test_invalid_manifest_is_rejected(tmp_path: Path) -> None:
    manifest_path = (
        tmp_path
        / "normalized"
        / "prices"
        / "source=yahoo"
        / f"security_id={SECURITY_ID}"
        / "manifest.json"
    )
    manifest_path.parent.mkdir(parents=True)
    manifest_path.write_text(json.dumps({"manifest_version": 1}), encoding="utf-8")

    with pytest.raises(DatasetSchemaError, match="invalid manifest"):
        ResearchCatalog(tmp_path).register()


def test_canonical_v1_preserves_strings_provenance_and_adds_numeric_views(
    tmp_path: Path,
) -> None:
    root = tmp_path / "normalized"
    _write_prices(root)
    _write_fundamentals(root)
    _write_macro(root)

    catalog = ResearchCatalog(tmp_path).register()
    canonical = catalog.connection.execute(
        "SELECT close, observed_precision, data_source_id, ingestion_run_id, raw_record_locator, "
        "normalizer_version FROM prices_canonical ORDER BY observed_at LIMIT 1"
    ).fetchone()
    numeric = catalog.connection.execute(
        "SELECT close_value, close_decimal, close, volume_value, volume_decimal "
        "FROM prices ORDER BY observed_at LIMIT 1"
    ).fetchone()

    assert canonical == (
        "100.123456789012345678",
        "unknown",
        SOURCE_ID,
        RUN_ID,
        "chart/result[0]",
        "go-v1",
    )
    assert numeric[0] == "100.123456789012345678"
    assert str(numeric[1]) == "100.123456789012345678"
    assert numeric[2] == pytest.approx(100.12345678901235)
    assert numeric[3] == "1000.25"
    assert str(numeric[4]) == "1000.250000000000000000"
    assert [item.file_count for item in catalog.status()] == [1, 1, 1, 0]


def test_new_observed_precision_is_preserved_in_canonical_and_research_views(
    tmp_path: Path,
) -> None:
    root = tmp_path / "normalized"
    _write_price_parts(root, [_price_select(observed_precision="second")])
    _write_fundamentals(root, observed_precision="date")
    _write_macro_rows(root, [_macro_select(observed_precision="date")])

    catalog = ResearchCatalog(tmp_path).register()

    assert catalog.connection.execute(
        "SELECT observed_precision FROM prices_canonical"
    ).fetchone() == ("second",)
    assert catalog.connection.execute(
        "SELECT observed_precision FROM fundamentals_canonical"
    ).fetchone() == ("date",)
    assert catalog.connection.execute(
        "SELECT observed_precision FROM macroeconomics_canonical"
    ).fetchone() == ("date",)
    assert catalog.connection.execute(
        "SELECT observed_precision FROM prices"
    ).fetchone() == ("second",)
    assert catalog.connection.execute(
        "SELECT observed_precision FROM fundamentals"
    ).fetchone() == ("date",)
    assert catalog.connection.execute(
        "SELECT observed_precision FROM macroeconomics"
    ).fetchone() == ("date",)


def test_mixed_old_and_new_observed_precision_parts_are_compatible(
    tmp_path: Path,
) -> None:
    root = tmp_path / "normalized"
    _write_price_parts(
        root,
        [
            _price_select(close="100"),
            _price_select(
                observed="2025-02-10 21:00:00Z",
                available="2025-02-11 00:00:00Z",
                close="110",
                observed_precision="second",
            ),
        ],
    )
    _write_fundamental_parts(
        root,
        [
            _fundamental_select(),
            _fundamental_select(observed_precision="date"),
        ],
    )
    _write_macro_parts(
        root,
        [
            _macro_select(),
            _macro_select(observed="2025-02-01 00:00:00Z", observed_precision="date"),
        ],
    )

    catalog = ResearchCatalog(tmp_path).register()

    assert catalog.connection.execute(
        "SELECT observed_precision FROM prices_canonical ORDER BY observed_at"
    ).fetchall() == [("unknown",), ("second",)]
    assert catalog.connection.execute(
        "SELECT observed_precision FROM fundamentals_canonical ORDER BY observed_precision"
    ).fetchall() == [("date",), ("unknown",)]
    assert catalog.connection.execute(
        "SELECT observed_precision FROM macroeconomics_canonical ORDER BY observed_at"
    ).fetchall() == [("unknown",), ("date",)]


@pytest.mark.parametrize("dataset", ["prices", "fundamentals", "macroeconomics"])
def test_invalid_observed_precision_is_rejected(tmp_path: Path, dataset: str) -> None:
    root = tmp_path / "normalized"
    if dataset == "prices":
        _write_price_parts(root, [_price_select(observed_precision="millisecond")])
    elif dataset == "fundamentals":
        _write_fundamentals(root, observed_precision="millisecond")
    else:
        _write_macro_rows(root, [_macro_select(observed_precision="millisecond")])

    with pytest.raises(DatasetSchemaError, match="invalid observed_precision"):
        ResearchCatalog(tmp_path).register()


@pytest.mark.parametrize("dataset", ["prices", "fundamentals", "macroeconomics"])
def test_wrong_observed_precision_physical_type_is_rejected(
    tmp_path: Path, dataset: str
) -> None:
    root = tmp_path / "normalized"
    if dataset == "prices":
        query = _price_select(observed_precision="second").replace(
            "'second'::VARCHAR AS observed_precision", "1::INTEGER AS observed_precision"
        )
        _write_price_parts(root, [query])
    elif dataset == "fundamentals":
        query = _fundamental_select(observed_precision="second").replace(
            "'second'::VARCHAR AS observed_precision", "1::INTEGER AS observed_precision"
        )
        _write_fundamental_parts(root, [query])
    else:
        query = _macro_select(observed_precision="second").replace(
            "'second'::VARCHAR AS observed_precision", "1::INTEGER AS observed_precision"
        )
        _write_macro_parts(root, [query])

    with pytest.raises(DatasetSchemaError, match="observed_precision.*physical type"):
        ResearchCatalog(tmp_path).register()


def test_missing_non_observed_precision_field_is_still_rejected(tmp_path: Path) -> None:
    root = tmp_path / "normalized"
    query = _price_select().replace("'yahoo'::VARCHAR AS source,", "")
    _write_price_parts(root, [query])

    with pytest.raises(DatasetSchemaError, match=r"missing canonical v1 field\(s\): source"):
        ResearchCatalog(tmp_path).register()


def test_deterministic_point_in_time_snapshot_excludes_future_observations(
    tmp_path: Path,
) -> None:
    root = tmp_path / "normalized"
    _write_prices(root)
    _write_fundamentals(root)
    _write_macro(root)
    catalog = ResearchCatalog(tmp_path).register()
    mapping = SecurityMapping(SECURITY_ID, ISSUER_ID, "TEST")

    frame = catalog.research_snapshot(
        decision_at="2025-02-11T00:00:00Z",
        mapping=mapping,
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    )

    assert catalog.available_mappings([mapping]) == (mapping,)
    assert frame["trading_date"].astype(str).tolist() == ["2025-01-10", "2025-02-10"]
    assert frame["close_value"].tolist() == ["100.123456789012345678", "110"]
    assert frame["fundamental_value_text"].tolist() == ["11.000000000000000001"] * 2
    assert frame["macro_value_text"].tolist() == ["2.100000000000000001"] * 2


def test_available_at_is_cutoff_even_when_published_at_is_earlier(
    tmp_path: Path,
) -> None:
    root = tmp_path / "normalized"
    _write_prices(root)
    _write_fundamentals(root)
    _write_macro_rows(
        root,
        [
            _macro_select(
                published="2025-01-01 00:00:00Z",
                available="2025-03-05 00:00:00Z",
                ingested="2025-03-06 00:00:00Z",
                value="99.000000000000000099",
                raw_hash="f" * 64,
            ),
            _macro_select(
                value="2.100000000000000001",
                raw_hash="c" * 64,
            ),
        ],
    )
    catalog = ResearchCatalog(tmp_path).register()

    frame = catalog.research_snapshot(
        decision_at="2025-02-11T00:00:00Z",
        mapping=SecurityMapping(SECURITY_ID, ISSUER_ID),
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    )

    assert frame["macro_value_text"].tolist() == ["2.100000000000000001"] * 2
    assert frame["macro_available_at"].astype(str).str.startswith(
        "2025-01-15"
    ).all()


def test_macro_selection_matches_go_postgres_precedence_order(tmp_path: Path) -> None:
    root = tmp_path / "normalized"
    _write_prices(root)
    _write_fundamentals(root)
    _write_macro_rows(
        root,
        [
            _macro_select(
                observed="2025-01-01 00:00:00Z",
                available="2025-01-30 00:00:00Z",
                ingested="2025-01-31 00:00:00Z",
                value="1.000000000000000001",
                revision=99,
                raw_hash="f" * 64,
            ),
            _macro_select(
                observed="2025-01-02 00:00:00Z",
                available="2025-01-02 00:00:00Z",
                ingested="2025-01-03 00:00:00Z",
                value="2.000000000000000002",
                raw_hash="a" * 64,
            ),
            _macro_select(
                observed="2025-01-02 00:00:00Z",
                available="2025-01-03 00:00:00Z",
                ingested="2025-01-04 00:00:00Z",
                value="3.000000000000000003",
                revision=1,
                raw_hash="b" * 64,
            ),
            _macro_select(
                observed="2025-01-02 00:00:00Z",
                available="2025-01-05 00:00:00Z",
                ingested="2025-01-06 00:00:00Z",
                value="4.000000000000000004",
                revision=1,
                raw_hash="c" * 64,
            ),
            _macro_select(
                observed="2025-01-02 00:00:00Z",
                available="2025-01-05 00:00:00Z",
                ingested="2025-01-07 00:00:00Z",
                value="5.000000000000000005",
                revision=1,
                raw_hash="d" * 64,
            ),
            _macro_select(
                observed="2025-01-02 00:00:00Z",
                available="2025-01-05 00:00:00Z",
                ingested="2025-01-07 00:00:00Z",
                value="6.000000000000000006",
                revision=1,
                raw_hash="e" * 64,
            ),
        ],
    )
    catalog = ResearchCatalog(tmp_path).register()

    frame = catalog.research_snapshot(
        decision_at="2025-02-01T00:00:00Z",
        mapping=SecurityMapping(SECURITY_ID, ISSUER_ID),
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    )

    assert frame["macro_value_text"].tolist() == ["6.000000000000000006"]


def test_price_revisions_are_selected_as_known_at_decision_time(tmp_path: Path) -> None:
    root = tmp_path / "normalized"
    manifest_path = _write_prices(root)
    directory = manifest_path.parent
    original_part = directory / json.loads(manifest_path.read_text(encoding="utf-8"))["parts"][0]["path"]
    path = directory / "revision.parquet"
    _write_parquet(
        path,
        _price_select(
            observed="2025-01-10 21:00:00Z",
            available="2025-03-05 00:00:00Z",
            close="101",
        ),
    )
    _write_manifest(
        manifest_path,
        [original_part, path],
        dataset="prices",
        source="yahoo",
        partition_key="security_id",
        partition_value=SECURITY_ID,
    )
    catalog = ResearchCatalog(tmp_path).register()
    mapping = SecurityMapping(SECURITY_ID, ISSUER_ID)

    before = catalog.research_snapshot(
        decision_at="2025-03-04T00:00:00Z",
        mapping=mapping,
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    )
    after = catalog.research_snapshot(
        decision_at="2025-03-06T00:00:00Z",
        mapping=mapping,
        fundamental_concept="Revenue",
        macro_series_id="GDP",
    )

    assert before.loc[before["trading_date"].astype(str) == "2025-01-10", "close"].item() == pytest.approx(100.12345678901235)
    assert after.loc[after["trading_date"].astype(str) == "2025-01-10", "close"].item() == 101
    assert before["trading_date"].is_unique and after["trading_date"].is_unique


def test_unsupported_schema_fails_with_migration_message(tmp_path: Path) -> None:
    unsupported_root = tmp_path / "unsupported" / "normalized"
    directory = unsupported_root / "prices" / "source=yahoo" / f"security_id={SECURITY_ID}"
    path = directory / "unsupported.parquet"
    _write_parquet(path, _price_select(schema_version="2.0.0"))
    _write_manifest(
        directory / "manifest.json",
        [path],
        dataset="prices",
        source="yahoo",
        partition_key="security_id",
        partition_value=SECURITY_ID,
    )
    with pytest.raises(DatasetSchemaError, match="schema_version 1.0.0.*migration required"):
        ResearchCatalog(tmp_path / "unsupported").register()


def test_non_utf8_or_invalid_decimal_fails_closed(tmp_path: Path) -> None:
    numeric_root = tmp_path / "numeric" / "normalized"
    query = _price_select().replace("'100.123456789012345678'::VARCHAR AS close", "100.0::DOUBLE AS close")
    numeric_directory = numeric_root / "prices" / "source=yahoo" / f"security_id={SECURITY_ID}"
    numeric_path = numeric_directory / "numeric.parquet"
    _write_parquet(numeric_path, query)
    _write_manifest(
        numeric_directory / "manifest.json",
        [numeric_path],
        dataset="prices",
        source="yahoo",
        partition_key="security_id",
        partition_value=SECURITY_ID,
    )
    with pytest.raises(DatasetSchemaError, match="non-UTF8: close"):
        ResearchCatalog(tmp_path / "numeric").register()

    invalid_root = tmp_path / "invalid" / "normalized"
    invalid_directory = invalid_root / "prices" / "source=yahoo" / f"security_id={SECURITY_ID}"
    invalid_path = invalid_directory / "invalid.parquet"
    _write_parquet(invalid_path, _price_select(close="1e2"))
    _write_manifest(
        invalid_directory / "manifest.json",
        [invalid_path],
        dataset="prices",
        source="yahoo",
        partition_key="security_id",
        partition_value=SECURITY_ID,
    )
    with pytest.raises(DatasetSchemaError, match="invalid canonical decimal.*close"):
        ResearchCatalog(tmp_path / "invalid").register()


def test_physical_sentinels_are_exposed_as_sql_null(tmp_path: Path) -> None:
    root = tmp_path / "normalized"
    _write_fundamentals(root, sentinel=True)
    _write_macro(root, sentinel=True)
    catalog = ResearchCatalog(tmp_path).register()

    assert catalog.connection.execute(
        "SELECT security_id, period_start FROM fundamentals_canonical"
    ).fetchone() == (None, None)
    assert catalog.connection.execute(
        "SELECT seasonal_adjustment, vintage_at FROM macroeconomics_canonical"
    ).fetchone() == (None, None)


def test_filings_register_exact_physical_contract_and_metadata(tmp_path: Path) -> None:
    manifest_path = _write_filing_parts(tmp_path / "normalized", [_filing_select()])
    catalog = ResearchCatalog(tmp_path).register()

    columns = catalog.connection.execute(
        "DESCRIBE SELECT * FROM filings_canonical"
    ).fetchall()
    assert [row[0] for row in columns] == [
        "schema_version",
        "id",
        "source",
        "issuer_id",
        "source_document_id",
        "document_url",
        "accession_number",
        "form_type",
        "category",
        "document_type",
        "species",
        "subject",
        "presentation_type",
        "primary_document",
        "amends_source_document_id",
        "filing_date",
        "period_end",
        "has_period_end",
        "observed_at",
        "has_observed_at",
        "observed_precision",
        "published_at",
        "has_published_at",
        "published_precision",
        "available_at",
        "effective_at",
        "has_effective_at",
        "ingested_at",
        "raw_payload_hash",
        "data_source_id",
        "ingestion_run_id",
        "raw_record_locator",
        "normalizer_version",
    ]
    canonical = catalog.connection.execute(
        """
        SELECT source_document_id, accession_number, category, document_type, species,
               subject, presentation_type, primary_document, amends_source_document_id,
               observed_precision, published_at, published_precision, available_at,
               raw_payload_hash, data_source_id, ingestion_run_id, raw_record_locator
        FROM filings_canonical
        """
    ).fetchone()
    assert canonical == (
        "cvm-ipe:1023:12345:v1",
        "12345",
        "Categoria 'exata'; ação",
        "Tipo exato",
        "Espécie exata",
        "Assunto exato",
        "Tipo_Apresentacao exato",
        "not-downloaded",
        None,
        "date",
        None,
        "unknown",
        canonical[12],
        "d" * 64,
        SOURCE_ID,
        RUN_ID,
        "zip=2026/member=ipe.csv/row=7",
    )
    assert canonical[12] is not None
    assert catalog.status()[-1].row_count == 1
    assert manifest_path.exists()


def test_filings_manifest_part_is_verified(tmp_path: Path) -> None:
    manifest_path = _write_filing_parts(tmp_path / "normalized", [_filing_select()])
    part_path = manifest_path.parent / json.loads(
        manifest_path.read_text(encoding="utf-8")
    )["parts"][0]["path"]
    part_path.write_bytes(part_path.read_bytes() + b"tampered")

    with pytest.raises(DatasetSchemaError, match="SHA-256 mismatch"):
        ResearchCatalog(tmp_path).register()


def test_filing_null_timestamps_and_optional_strings_do_not_leak_epoch(
    tmp_path: Path,
) -> None:
    _write_filing_parts(
        tmp_path / "normalized",
        [
            _filing_select(
                accession_number="",
                category="",
                document_type="",
                species="",
                subject="",
                presentation_type="",
                primary_document="",
                amends_source_document_id="",
                has_period_end=False,
                has_observed=False,
                observed_precision="unknown",
                has_published=False,
                published_precision="unknown",
                has_effective=False,
            )
        ],
    )
    catalog = ResearchCatalog(tmp_path).register()

    canonical = catalog.connection.execute(
        """
        SELECT accession_number, category, document_type, species, subject,
               presentation_type, primary_document, amends_source_document_id,
               period_end, observed_at, published_at, effective_at,
               available_at
        FROM filings_canonical
        """
    ).fetchone()
    research = catalog.connection.execute(
        "SELECT period_end, observed_at, published_at, effective_at FROM filings"
    ).fetchone()
    assert canonical[:12] == (None,) * 12
    assert research == (None, None, None, None)
    assert canonical[12] is not None


def test_filings_as_of_uses_availability_and_observed_cutoffs(tmp_path: Path) -> None:
    _write_filing_parts(
        tmp_path / "normalized",
        [
            "\nUNION ALL\n".join(
                [
                    _filing_select(
                        source_document_id="cvm-ipe:1023:base:v1",
                        available="2026-01-08 00:00:00Z",
                        ingested="2026-01-08 00:00:00Z",
                        raw_hash="e" * 64,
                    ),
                    _filing_select(
                        source_document_id="cvm-ipe:1023:old-period:v1",
                        period_end="2000-12-31",
                        observed="2000-12-31 00:00:00Z",
                        available="2026-02-01 00:00:00Z",
                        ingested="2026-02-01 00:00:00Z",
                        raw_hash="f" * 64,
                    ),
                    _filing_select(
                        source_document_id="cvm-ipe:1023:future-observed:v1",
                        observed="2027-01-01 00:00:00Z",
                        available="2026-01-08 00:00:00Z",
                        ingested="2026-01-08 00:00:00Z",
                        raw_hash="1" * 64,
                    ),
                ]
            )
        ],
    )
    catalog = ResearchCatalog(tmp_path).register()

    before_late = catalog.filings_as_of(
        decision_at="2026-01-15T00:00:00Z", issuer_id=ISSUER_ID
    )
    after_late = catalog.filings_as_of(
        decision_at="2026-03-01T00:00:00Z", issuer_id=ISSUER_ID
    )

    assert before_late["source_document_id"].tolist() == ["cvm-ipe:1023:base:v1"]
    assert after_late["source_document_id"].tolist() == [
        "cvm-ipe:1023:base:v1",
        "cvm-ipe:1023:old-period:v1",
    ]
    assert after_late["published_at"].isna().all()
    assert "future-observed" not in after_late["source_document_id"].tolist()


def test_filings_as_of_excludes_current_cad_only_in_historical_mode(
    tmp_path: Path,
) -> None:
    _write_filing_parts(
        tmp_path / "normalized",
        [
            "\nUNION ALL\n".join(
                [
                    _filing_select(
                        source_document_id="cvm-ipe:1023:historical:v1",
                        raw_hash="2" * 64,
                    ),
                    _filing_select(
                        source_document_id="cvm-cad:1023:2026-01-08",
                        form_type="cvm_cad",
                        has_observed=False,
                        observed_precision="unknown",
                        raw_hash="3" * 64,
                    ),
                ]
            )
        ],
    )
    catalog = ResearchCatalog(tmp_path).register()

    historical = catalog.filings_as_of(
        decision_at="2026-01-15T00:00:00Z", issuer_id=ISSUER_ID
    )
    replay = catalog.filings_as_of(
        decision_at="2026-01-15T00:00:00Z",
        mode="installation_replay",
        issuer_id=ISSUER_ID,
    )

    assert historical["source_document_id"].tolist() == ["cvm-ipe:1023:historical:v1"]
    assert replay["source_document_id"].tolist() == [
        "cvm-cad:1023:2026-01-08",
        "cvm-ipe:1023:historical:v1",
    ]


def test_filings_keep_versioned_source_document_identity(tmp_path: Path) -> None:
    _write_filing_parts(
        tmp_path / "normalized",
        [
            "\nUNION ALL\n".join(
                [
                    _filing_select(
                        source_document_id="cvm-ipe:1023:12345:v1",
                        raw_hash="4" * 64,
                    ),
                    _filing_select(
                        filing_id="4c3d88f5-55b8-4dc5-a6be-2f77e9e99201",
                        source_document_id="cvm-ipe:1023:12345:v2",
                        category="Categoria versão 2",
                        raw_hash="5" * 64,
                    ),
                ]
            )
        ],
    )
    catalog = ResearchCatalog(tmp_path).register()

    assert catalog.connection.execute(
        "SELECT source_document_id, category FROM filings ORDER BY source_document_id"
    ).fetchall() == [
        ("cvm-ipe:1023:12345:v1", "Categoria 'exata'; ação"),
        ("cvm-ipe:1023:12345:v2", "Categoria versão 2"),
    ]


def test_current_yaml_mapping_is_loaded_without_historical_resolution(
    tmp_path: Path,
) -> None:
    config = tmp_path / "config.yaml"
    config.write_text(
        f"""
universe:
  - security_id: {SECURITY_ID}
    issuer_id: {ISSUER_ID}
    ticker: AAPL
    legal_name: Apple Inc.
""",
        encoding="utf-8",
    )

    mappings = load_security_mappings(config)

    assert mappings == (
        SecurityMapping(SECURITY_ID, ISSUER_ID, "AAPL", "Apple Inc."),
    )
    assert "current YAML" in (load_security_mappings.__doc__ or "")
    assert "not historical" in (SecurityMapping.__doc__ or "")
