# v1.0 data-fitness matrix acceptance — 2026-08-30

## Result

The v1.0 release now carries a machine-readable historical-fitness registry at
[`release/data-fitness.json`](../../release/data-fitness.json). The registry is
validated by `research.data_fitness` and pinned as the `data-fitness` entry in
[`release/compatibility.json`](../../release/compatibility.json). The implementation
and release wiring landed in `4a72ed0` (`feat(release): pin data fitness matrix`).

The matrix contains 29 entries covering:

- all five datasets exposed by `ResearchCatalog`;
- all eight input kinds accepted by the backtest loader;
- all four registered feature sets; and
- the cross-market commodity evidence consumed by the workflow harness.

Every entry records its authority/access boundary, coverage and gaps, observed and
available-time semantics, revision policy, historical-fitness label, identity policy,
quality checks, evidence, and consumers. The registry is explicit about two
important policies:

```text
unknown_source_policy = reject_unlisted
backtest_admission    = backtest_safe_only
```

The matrix classifies accepted bounded fixtures and source paths without turning
bounded evidence into a broad coverage claim. Current-vintage FRED/BCB, receipt-time
Yahoo/B3 prices, the B3 action replay, CVM IPE replay, and the copper workflow fixture
remain current/replay-only or blocked as recorded in their entries.

## Fail-closed checks

The validator rejects:

1. an unknown matrix field;
2. a missing backtest input-kind classification;
3. an evidence path outside the checked-in repository; and
4. a source-code list that does not match the entries.

The focused contract run passed:

```text
11 tests passed
All checks passed! (Ruff)
```

The release preflight also passed with 61 pinned schemas, 3 registries, and 16
migrations. This closes the v1 data-fitness classification gate for the repository's
declared research/backtest surfaces; it does not make any source with a
`current_research_only`, `installation_replay_only`, or `unsupported` label eligible
for historical backtesting.

## Remaining release boundary

The full v1.0 release remains pending a genuine recent wall-clock paper session.
The matrix is a prerequisite for that acceptance, not a substitute for live
forward evidence or broader provider coverage. The repository-side install and
upgrade lifecycle is covered separately by the
[v1 installation lifecycle acceptance report](2026-08-30-v1-install-upgrade.md).
