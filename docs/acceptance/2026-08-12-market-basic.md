# Market-basic operator acceptance — 2026-08-12

This report records the first end-to-end publication of the deterministic
`market-basic` feature set through the supported operator command. Runtime data
under `data/features/` remains intentionally ignored by Git; the hashes below make
the retained local artifact independently identifiable.

## Implementation boundary

- Operator command commit: `e581c5d` (`feat(features): add operator publication command`)
- Documentation-aligned generation commit: `37e881250e7c967952b708e6926e48260d84cbeb`
- Security: `469fc20f-7d4b-45bb-b827-05f8410e71aa` (configured AAPL listing)
- Decision timestamp: `2026-08-12T21:00:01Z`
- Computation delay: 30 seconds

The artifact was published with:

```sh
make feature \
  SECURITY_ID=469fc20f-7d4b-45bb-b827-05f8410e71aa \
  DECISION_AT=2026-08-12T21:00:01Z \
  FEATURE_DELAY=30
```

## Retained artifact

- Artifact ID: `e66fd547-1435-50a0-a3c5-b244c692e045`
- Manifest path: `data/features/market-basic/1.0.0/artifact-e66fd547-1435-50a0-a3c5-b244c692e045/manifest.json`
- Manifest SHA-256: `f730e0fb66f15385f4f803de9d40b27a49c06a908723045b7799e25d4ae132a1`
- Output part SHA-256: `958cabd86232b1f83a2122f30b1e9f24127d94b1e54960173dcc2358e13ce6ad`
- Input fingerprint: `665c5775d103fef5824132ceb3abbc90bd3ca5c73efb5b8c4822531effdd79d4`
- Selected price manifest SHA-256: `7041a5d8eb30d5ce048fc4918304256820bf4d81a075fafc07feca813500dd18`
- Selected price part SHA-256: `aeab98e80c734af189864a01dfeacb16be8c1077c73f6f870b83b7a36a4c8494`
- Maximum input availability: `2026-08-12T20:07:52.603875Z`
- Derived availability: `2026-08-12T20:08:22.603875Z`

The exact feature values were:

| Feature | Value |
| --- | --- |
| `close` | `302.25` |
| `return_1d` | `-0.00872389764245689691686320129193615176513880955383087871209908844630230057841641733677413320873858220270334188057914232959707924` |
| `range_1d` | `5.08999633789065` |
| `volume` | `37377128` |

## Acceptance checks

- Immediate manifest and Parquet readback through `make feature-validate` passed.
- Repeating the identical publication returned the same artifact and left the
  manifest modification time, size, and SHA-256 unchanged.
- A decision timestamp one microsecond before the input availability boundary,
  `2026-08-12T20:07:52.603874Z`, failed closed with no eligible price rows.
- Feature tests cover changed-input identity conflicts, tampered parts, unlisted
  files, unsupported versions/features, exact decimal behavior, and missing inputs.
- `make validate` passed: all Go tests and vet, 13 JSON Schemas, 49 Python tests,
  Ruff, executable notebook, dashboard SQL smoke checks, and PostgreSQL/collector/
  Jupyter image builds.
- The rebuilt long-lived PostgreSQL, Jupyter, and Grafana services are healthy.
  Grafana remains bound locally at `127.0.0.1:3001` because host port 3000 is in use.
- The installed `invs-feature` console command is present in the rebuilt Jupyter
  container.

## Operational cleanup

The pre-existing FRED run `65fd1102-0ed9-48d8-aa5a-36329353aaf5` was confirmed
orphaned: it had remained `running` with zero records since
`2026-08-12T18:57:05Z`, and no collector container remained. It was explicitly
cancelled at `2026-08-13T01:46:58Z` with that reason. PostgreSQL then reported zero
queued or running ingestion runs.

This acceptance establishes an operational, reproducible feature-publication loop.
It does not establish a strategy, backtest, portfolio, ML, or historical-vintage
claim.
