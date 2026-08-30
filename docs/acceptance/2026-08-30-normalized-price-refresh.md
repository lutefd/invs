# v1.0 normalized price refresh and lineage repair — 2026-08-30

This note records the live installation-replay data repair performed while
preparing the v1.0 forward-paper boundary. It is not a historical-publication or
live-performance claim.

## Trigger

The existing Yahoo normalized partition still carried the former `raw` price
basis. The current Yahoo chart collector publishes `split_adjusted` prices, so
the normalizer correctly refused to merge the new receipt with the legacy
partition and required an archive-and-reingest boundary. The existing
`market-basic` artifact also pointed at the archived manifest and part; leaving
it active would have kept reconciliation non-zero.

## Recoverable repair

The complete pre-refresh normalized tree was moved, without deleting raw
evidence or PostgreSQL metadata, to:

```text
/home/luis/invs-acceptance/2026-08-30-normalized-yahoo-legacy
```

The former Yahoo price manifest and part remain at:

| Object | SHA-256 |
| --- | --- |
| `prices/source=yahoo/security_id=469fc20f-7d4b-45bb-b827-05f8410e71aa/manifest.json` | `1c2ae330abfc690f3f9e20c57c0c76abc4c6a3382702882093cf3becb07c932e` |
| `prices/source=yahoo/security_id=469fc20f-7d4b-45bb-b827-05f8410e71aa/part-358a64fc9fb282b096e993aa1135ecc42305dd3b74ffe6cd5fe40331281767e3.parquet` | `358a64fc9fb282b096e993aa1135ecc42305dd3b74ffe6cd5fe40331281767e3` |

The stale derived artifact was separately preserved at:

```text
/home/luis/invs-acceptance/2026-08-30-feature-legacy
```

Its legacy manifest and output part hashes are:

| Object | SHA-256 |
| --- | --- |
| `market-basic/1.0.0/artifact-d2c49f5e-3686-5121-be29-d8cc095af5c2/manifest.json` | `ab652ad7853f3af1010541f582d5223791b38c8d2e77351d79e14988e1f413d1` |
| `market-basic/1.0.0/artifact-d2c49f5e-3686-5121-be29-d8cc095af5c2/part-da84dcfc454462e40aec011b4f5283b2f20bea7220fed71b9b1a5c3ac3d70fc1.parquet` | `da84dcfc454462e40aec011b4f5283b2f20bea7220fed71b9b1a5c3ac3d70fc1` |

That artifact is retained as legacy evidence and is not part of the active
feature root. It predates the current strict feature contract and is not
promoted into current research.

## Successful reingest

The supported command was:

```sh
INVS_BIND_ADDRESS=127.0.0.1 make ingest \
  SOURCE=prices \
  RUN_KEY=v1-forward-bootstrap-20260830
```

The resulting Yahoo run is:

| Field | Value |
| --- | --- |
| Ingestion run | `fa61bbae-7861-4990-8bef-0cc2712dceb4` |
| Raw run manifest | `data/raw/runs/yahoo/fa61bbae-7861-4990-8bef-0cc2712dceb4/manifest.json` |
| Raw run manifest SHA-256 | `8fb199c8b6af909bdb45114d2864d992f9533453b1474b96a2c590a5ecde1a14` |
| Raw object SHA-256 | `a4f0261f830f3855cb58563fbdfda32bfb91390493d5ce5c67cfcd7cd510aa4f` |
| Normalized manifest SHA-256 | `dfd5e0b4920a23d0d195384e5a3151fa7be0c1622369021e5ebda0d895d63dfb` |
| Normalized part SHA-256 | `abc8c203260fa16a4355c6b4b5b4766cd0f2e4001602d277a59a00ed3e3a6373` |
| Normalized rows | `1,673` |
| Latest observed session | `2026-08-28T20:00:00Z` |
| Receipt/availability | `2026-08-30T08:13:17.607907Z` |
| Price basis | `split_adjusted` |

This data remains `installation_replay_only` with conservative receipt-time
availability. It must not be used to backdate a wall-clock forward record.

## Verification

```text
INVS_BIND_ADDRESS=127.0.0.1 make reconcile
reconciliation report generated_at=2026-08-30T08:19:45Z issues=0 data_root=/data
```

The live PostgreSQL, Jupyter, and Grafana services remained healthy. The active
feature root now contains only its `.gitkeep`; a new feature artifact will be
published only with an exact resolver-backed XNAS calendar pin and an explicit
decision schedule. The v1.0 release still requires a genuinely recent
wall-clock paper session and final workflow acceptance.
