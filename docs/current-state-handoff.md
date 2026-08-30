# Personal Quant Research Platform — Current State Handoff

This document records the repository as it exists at the handoff boundary. It is
intended for the next engineering session, not as a product brochure. The
repository is the first actively developed version of the platform. It is not a
deprecated production version.

## Post-handoff continuation update

On 2026-08-13, the first three immediate actions from this handoff were completed:

- README, architecture, schema guidance, and ADR wording were aligned with the
  accepted CVM IPE boundary and implemented `market-basic` engine.
- The lingering zero-record FRED run was confirmed orphaned and explicitly
  cancelled; PostgreSQL subsequently reported zero queued/running runs.
- A supported `make feature` / `make feature-validate` operator path was added and
  exercised against the real manifest-backed AAPL slice, including identical replay
  and the exact point-in-time cutoff.

The durable evidence is in
[the market-basic operator acceptance report](acceptance/2026-08-12-market-basic.md).

The next data-integrity milestone has now also been implemented:

- ALFRED is a separate disabled-by-default macro source using the official FRED
  observations endpoint with historical real-time periods and explicit replay bounds.
- Raw JSON pages are persisted before canonical publication, with per-page hashes,
  non-secret run inputs, deterministic per-observation revisions, explicit null
  vintages, and a conservative 36-hour availability policy for date-only vintages.
- Historical ALFRED writes are idempotent and preserve A -> B -> A sequences without
  revision drift. Current FRED and ALFRED series remain source-separated in research.
- PostgreSQL's latest macro projection now permits an explicit null value; authoritative
  historical vintages remain in manifest-backed Parquet.
- Provider, collector, normalizer, metadata, research-cutoff, and dashboard tests
  pass. A bounded credentialed CPIAUCSL live run, exact-key skip, and independent
  zero-row canonical replay also passed; see the
  [ALFRED acceptance report](acceptance/2026-08-13-alfred-cpiaucsl.md).

The first v0.1 data-integrity unit then landed in `8f2680f`: every provider now
returns a common downloaded-resource collection alongside parse errors, and the
collector persists that collection before finalizing a failed resource. Contract
tests cover malformed Yahoo, FRED, BCB, SEC, ALFRED, and CVM responses, including
partial multi-resource downloads.

The platform now has a canonical [full-version roadmap](full-version-roadmap.md) and
a granular [roadmap execution index](roadmap/README.md), introduced at `cec0ed4`.
The roadmap, rather than the old numbered actions at the bottom of this historical
handoff, is the sequencing authority for future versions. The notebook now has
separate empty-safe CVM filing and feature-artifact inspection sections from
`806874a`; no strategy, backtest, portfolio, or ML boundary was introduced.

The recovery slice then landed in `0f73e39`: a read-only reconciliation report,
explicit destination backup/restore scripts, and a clean-root PostgreSQL/data
restore drill. The v0.1 foundation and operations gate is now accepted at
`63d479d`; the exact evidence is in the
[v0.1 foundation acceptance report](acceptance/2026-08-13-v0.1-foundation.md).
The v0.2 historical-truth phase is now accepted; strategy and execution logic remain
later roadmap boundaries.

The first v0.2 contract slice landed in `4d483ac`: ADRs 0006 and 0007 define
source-backed historical identity/universe semantics and versioned exchange
calendars/decision clocks; strict schemas and pure Python resolvers exercise
synthetic US and Brazil fixtures. The slice deliberately stops before live
calendar/source admission and the bounded bias audit.

The durable metadata implementation slice then landed in `d963180`
(`feat(data): publish historical truth metadata boundary`). It adds the Go
publication/resolution boundary, five append-only PostgreSQL tables with
revision-aware exclusion constraints and immutable record hashes, Docker/init and
idempotent migration wiring, focused Go tests, and a real PostgreSQL harness. This
is an accepted implementation boundary, not source admission or the v0.2 exit gate.
Later slices admitted bounded membership and current/reference calendars. The
follow-up historical identity slice publishes source-backed INSM/PETZ3
identifier/listing intervals and a PETZ3 trading-cessation correction with exact
knowledge-time resolution. The historical calendar follow-up now admits exact
Nasdaq/B3 artifacts, publication/correction chronology, decision-time selection,
and feature-artifact clock pins. The corporate-action follow-up now publishes one
exact SEC split, retains a B3 sample revision family only from installation receipt,
blocks its unsupported latest state, and emits immutable adjustment artifacts from
resolver-backed snapshots. The PTAX follow-up now collects exact BCB closing
bulletins, publishes canonical USD/BRL history, selects an explicit fixing date at
the source timestamp, and emits sell-side direct/inverse conversions with complete
input pins. The SEC follow-up now publishes accession-keyed filing metadata from
submissions, uses exact EDGAR acceptance as availability, preserves source civil
dates and nested primary documents, and exposes the rows through strict
`filings_as_of`. The official B3 COTAHIST follow-up closes the Brazil price bridge
with exact ticker+ISIN mapping, raw prices, and receipt-time availability. The final
combined audit now passes 13 exact US/Brazil probes, verifies 16 retained artifacts,
and closes v0.2 without adding a strategy or backtester.

The first v0.3 feature-platform entry slice is now implemented in four reviewable
commits: ADR 0010 at `1a951e5`, the checked-in registry and strict resolver at
`1f8f270`, the resumable dataset-level batch manifest at `a8c2302`, and the
operator CLI/Makefile path at `f1792ad`. The bounded producer still dispatches only
the accepted `market-basic` 1.0.0 implementation. It records the explicit security
list, decision schedule, registry hash, accepted historical-fitness label
(`installation_replay_only` for receipt-time prices), input lineage, child output
parts, and rejected partitions. This is a v0.3 entry boundary, not the complete
feature-platform exit gate.

The next catalog slice is now implemented through `5e580d2` (feature-artifact
metadata), `3d63d21` (input-fitness lineage), `14d5a5f` (the operator collector),
and `f800527` (universe-lineage correction), with the decision recorded in ADR 0011
at `12fdf56`. The bounded `make feature-catalog` path revalidates an immutable batch
and its child manifests/parts, then records one PostgreSQL registration envelope with
decision points, universe members, selected inputs, fitness labels, and accepted
partition pointers. Feature rows remain in Parquet; repeated registration of the same
envelope is an idempotent no-op.

The catalog reporting continuation is now implemented through `03521c2`, `731b0b4`,
and `94a3bf0`. The read-only `make feature-report` path emits text or a versioned JSON
report with complete/partial/empty/inconsistent coverage status, expected versus
accepted partitions by decision timestamp, input fitness, and manifest/part lineage.
It uses a repeatable-read PostgreSQL transaction and never reads feature values. The
report schema is `schemas/feature-catalog-report.schema.json`; a live collector-image
probe covered first registration, idempotent retry, JSON output, and text output for a
two-date batch. This still does not claim feature-level null reporting, new feature
families beyond the first market/risk family, or the v0.3 exit gate.

The first market/risk family is now implemented through the contract commit
`dd450b3` and producer commit `941c6b5`. `market-momentum` 1.0.0 publishes four
eligible-observation close returns, annualized trailing 21-return volatility, and
trailing 21-close maximum drawdown with explicit warmup/null behavior. Its strict
schemas, formulas, point-in-time boundary, and receipt-time `installation_replay_only`
fitness are recorded in ADR 0012 and the
[implementation acceptance note](acceptance/2026-08-29-market-momentum.md).

## v0.3 completion boundary

The v0.3 feature-platform exit gate is now accepted on 2026-08-29. The continuation
chain added the reviewed SEC taxonomy registry and mappings (`53fe4fe`), canonical
fundamental and macro point-in-time selectors (`ccb9cbf`), registered
`fundamental-growth` and `macro-state` contracts (`4827537`), their strict producers
and schemas (`eee7fe1`), multi-dataset batch dispatch (`c126d39`), and the read-only
feature-quality report (`54a00d2`, decision-clock correction `0454d0e`).

The acceptance fixture at `5fc3783` publishes `market-basic`, `fundamental-growth`,
and `macro-state` for 20 deterministic securities at two monthly decisions. It
prepublishes one child per family, resumes the remainder, crosses an ALFRED revision
boundary (`0.1` before the revision and `0.15` after it), compares the interrupted
and clean feature trees byte-for-byte, and fails closed for input-part, output-part,
registry, taxonomy, and universe tampering. The quality report applies availability,
observed-time, period-end, and vintage cutoffs before explaining typed nulls and raw
locators. The exact commands and evidence are in the
[v0.3 acceptance report](acceptance/2026-08-29-v0.3-feature-platform.md).

The v0.3 scope is complete. Valuation, broader taxonomy coverage, feature notebooks,
Grafana freshness panels, automatic orphan repair, strategies, and backtesting remain
later roadmap boundaries.

## v0.4 completion boundary

The v0.4 theme-intelligence and hypothesis-loop gate is now accepted on 2026-08-29.
The implementation chain added ADR 0015 and the append-only research schema in
`4752a5d`, the PostgreSQL repository in `8b9e4e7`, immutable document artifacts in
`a64e86e`, reviewed event proposals in `cb64fd5`, point-in-time evidence packs and
memo round trips in `17cbbc2`, theme context references in `a84a2b2`, and the
reviewed AI-infrastructure fixture in `161a23e`. The metadata CLI and read models
landed in `897e7d8` and `b6d6bd3`; policy-pinned measurement and contract hardening
landed in `a486240` and `cb93e52`.

The acceptance boundary at `3ac61e1` runs `make research-acceptance` against a fresh
temporary database. It seeds seven reviewed theme nodes and six relationships,
reconstructs the decision-date snapshot, retains a raw/text document pair, rejects
one bad event proposal and accepts one correct proposal, rejects a future evidence
reference, round-trips a memo, freezes a bounded prediction, rejects direct frozen
mutation, and measures the later outcome under a pinned policy. Database checks also
cover relationship validity, revision gaps, append-only revisions, and review
authorization. The deterministic snapshot correction is `0b97702`.

The exact evidence and validation ladder are recorded in the
[v0.4 hypothesis-loop acceptance report](acceptance/2026-08-29-v0.4-hypothesis-loop.md).
The v0.4 scope is complete. At that boundary, strategies, backtesting, portfolio
construction, paper trading, and live execution remained later roadmap boundaries;
the v0.5 and v0.6 sections below record the subsequent accepted slices.

## v0.5 completion boundary

The v0.5 point-in-time backtesting and experiment-tracking gate is accepted on
2026-08-29 at `180d5c9`. The implementation now includes strict hash-pinned
experiment specifications, explicit decision and execution clocks, a daily
long-only event loop, exact cash/positions/NAV accounting, corporate actions, FX,
conservative costs and delays, transparent buy-and-hold/equal-weight/momentum
baselines, versioned metrics and risk-free policy, immutable result artifacts,
comparison, PostgreSQL experiment/run lineage, and authenticated checkpoint resume.

The retained acceptance runs five bounded US/Brazil experiments, proves clean replay
and input-perturbation identity behavior, exercises the isolated metadata retry and
append-only path, and passes 13 deliberate lookahead/survivorship probes. The exact
IDs, returns, costs, fixtures, commands, and validation ladder are recorded in the
[v0.5 backtesting acceptance report](acceptance/2026-08-29-v0.5-backtesting.md).

The internal Python simulator remains canonical. LEAN is not a v0.5 runtime
dependency and no semantic-equivalence claim was made. Rolling calibration, broker
submission, intraday simulation, shorting, and margin remained later boundaries at
the v0.5 exit; portfolio construction and internal paper trading are accepted in
the v0.6 section below.

## v0.6 completion boundary

The v0.6 portfolio-construction and paper-trading gate is accepted at validation
boundary `95a79b5`, with feature implementation boundary `f02a57f`. The slice was
delivered through the ADR/schema boundary in
`734ceef`, the forward portfolio and ledger engine in `3d9b461`, the operator CLI in
`2f05b93`, the PostgreSQL catalog in `65da807`, the fractional-reserve correction in
`1eacda9`, the deterministic recovery drill in `d2fbee9`, the catalog acceptance in
`d92a15e`, and the paper dashboard in `f02a57f`.

The retained acceptance replays 22 sessions for separate equal-weight and momentum
accounts. It proves five rebalances, no-op sessions, manual and auto approval,
close-to-next-open fills, split/dividend/delisting handling, duplicate proposal and
approval idempotency, stale-data halting, risk rejection without orders, exact ledger
rebuild, backup restore, and reconciliation. The PostgreSQL acceptance proves
idempotent account/event registration, append-only guards, sequence enforcement, and
latest-event reporting. Evidence and commands are recorded in the
[v0.6 paper-trading acceptance report](acceptance/2026-08-29-v0.6-paper-trading.md).

The accepted boundary is an internal, recorded/replayed forward-paper process. It
does not include real-money or broker submission, intraday execution, leverage,
shorting, margin, or a claim of live performance.

## v1.0 pre-release integration boundary

The v1.0 integration work is in progress as of 2026-08-30. The compatibility
contract, specification-driven daily-cycle runner, integrated thematic/cross-market
workflow reports, security and recovery controls, and resilience/bias acceptance
landed in `f8abe40`, `31e34b8`, `d5b9c22`, `586d79e`, and `9a0f558` respectively.
The CLI-level daily-cycle failure/resume acceptance followed in `3f96623`. The
hash-pinned genuine forward-record contract and capture CLI landed in `e272e3d`,
with the import-isolation correction in `b471908` and full paper-account/report
validation plus append-only ledger replay in `be16303`. The repeatable replay-only
forward-record rejection guard landed in `cf37708`.

The daily-cycle preflight was then hardened in `a4f8868` to require the complete
v1 paper-account envelope, reject missing or unknown top-level fields and wrong
schema versions before execution, and cover the failure path with a focused test
and acceptance fixture. The full `make validate` and latest resilience, installation,
and workflow reruns passed from `c79711d`; the integrated workflow still
intentionally reports `attention` until genuine forward evidence exists.

The [v1 resilience acceptance report](acceptance/2026-08-30-v1-resilience.md) passes
the retained historical-bias and paper-ledger checks, CLI-level daily-cycle
failure/resume behavior, disposable backup integrity, replay-only forward-record
rejection, and live backup → clean-root PostgreSQL restore →
reconciliation path. The [v1 workflow integration report](acceptance/2026-08-30-v1-workflow-integration.md)
links the research, backtest, and paper layers for thematic and US/Brazil
cross-market scenarios, but intentionally remains `attention` because the retained
paper evidence is recorded/replayed.

The [v1 installation lifecycle acceptance report](acceptance/2026-08-30-v1-install-upgrade.md)
passes the isolated fresh install, additive pre-v1 upgrade, idempotent migration
reapply, PostgreSQL/data backup and restore, tamper rejection, and daily-cycle
interruption/resume stages at `c011ab6`. Its Docker project, volume, and restore
database are disposable and are removed after the run; the live `invs` volume is
not part of the test.

While preparing the forward-paper boundary on 2026-08-30, the Yahoo normalized
partition reached the current `split_adjusted` price contract. The former
normalized tree and its legacy `market-basic` artifact were preserved in the
recoverable archives documented in the [normalized price refresh acceptance
note](acceptance/2026-08-30-normalized-price-refresh.md), then the preserved raw
Yahoo evidence was reingested. The new receipt-time partition has 1,673 rows
through the 2026-08-28 session, and `make reconcile` returned `issues=0`. This is
installation-replay data preparation only; it is not a wall-clock forward record.

The current host refresh then ran `INVS_BIND_ADDRESS=127.0.0.1 make ingest
SOURCE=all` for every enabled source. `make ops-status` reported `ok` with current
source/projection freshness and 17% data-disk usage; `make reconcile` again returned
`issues=0`; the notebook and dashboard smoke checks passed; and a validated backup
was retained outside the checkout at `/home/luis/invs-backups/v1-current-20260830-0917`.
The exact source hashes and backup fingerprint are in the [current operational
readiness note](acceptance/2026-08-30-v1-live-operations.md). This confirms current
host readiness only and does not create a genuine wall-clock paper record.

The forward paper input boundary now admits explicitly labeled `split_adjusted`
price artifacts while keeping historical backtests raw-only. A paper artifact must
use one price basis, and the loader rejects split-adjusted prices paired with
corporate actions to prevent double adjustment. This contract and its regressions
landed in `a1ba6c7`, `22e71cf`, and `7153626`; the full `make test` gate then passed
210 Python tests with the release manifest and schema catalog aligned, including
the complete daily-cycle preflight and input-byte resumability checks.
The follow-up legacy-report compatibility regression in `59c435f` brought the
current full gate to 211 Python tests. The timestamp-bound acceptance regressions
in `c12074a` brought the current full gate to 213 Python tests; the active v1
evidence index records that latest result.

The supported forward-evidence path is `make forward-record-capture` after a real
recent paper session. Newly generated paper reports carry an invocation-time UTC
`recorded_at`; capture requires it to follow the risk check within 24 hours and to
be no later than the capture timestamp. The command validates the append-only
ledger and reconciled report, records account/report/manifest hashes, and refuses
stale, late, or replay-only evidence; no genuine wall-clock record exists in the
repository yet. This fail-closed report-time binding landed in `b5200fa`. The
aggregate acceptance path remains compatible with retained pre-timestamp replay
reports, covered by the regression in `59c435f`; those reports remain ineligible
for genuine capture. The pre-risk and post-capture rejection branches are covered
by acceptance tests in `c12074a`.

The aggregate report needed by the v1 workflow is now derived from the same ledger
by `make paper-acceptance-report`, landed in `b450f9f`, with its release hash
refreshed in `c35ee45`. The command is read-only against the source account and
proves duplicate-cycle idempotency, rebuild equality, isolated backup/restore, and
reconciliation; a halted or otherwise incomplete session remains `attention`.
The workflow harness output-isolation correction landed in `97e9029`: replay runs
retain the fixed acceptance reports, while a genuine record defaults to its own
`data/research/acceptance/v1/genuine/<record_id>/` directory.

The final v1.0 release boundary is still pending a genuine wall-clock forward paper
record and final end-to-end workflow acceptance. The pre-release evidence index is
recorded, while the bounded Brazil and commodity fitness limitations remain in
force; no broker or real-money execution claim is made.

The data-fitness classification gate is now accepted at `4a72ed0`. The release-pinned
[`data-fitness matrix`](../release/data-fitness.json) covers five catalog datasets,
eight backtest input kinds, four feature families, and the workflow commodity
evidence path. Its fail-closed validator rejects unlisted sources and keeps
historical backtests restricted to `backtest_safe` inputs. This closes the
classification gate without upgrading the explicitly current/replay-only Yahoo,
B3, FRED, BCB, CVM, corporate-action, or commodity limitations.

## Yahoo `.SA` source-admission verification

The bounded live verification is recorded in the
[Yahoo `.SA` source-admission report](acceptance/2026-08-13-yahoo-sa-security-master.md).
Yahoo currently proves useful for a configured São Paulo quote and daily price
series, but it is **not admitted as historical security-master evidence**. The
checked response path did not provide stable issuer/security identity, ISIN/MIC,
primary-listing state, historical validity intervals, universe membership events,
revision history, or historical public-availability timestamps. Yahoo's published
terms also require a separate permission review for unattended automated collection
and restrict redistribution.

Yahoo `.SA` remains a candidate **price bridge only**, subject to that terms review.
The bounded official B3 path now owns current/reference Brazil identity and listing
evidence, including exact ISINs, through the raw-first collector and historical
metadata boundary. Separate official B3 portfolio and Plantao notices now provide
one accepted Ibovespa add/remove chain plus PETZ3 identifier/listing history through
trading cessation. This is a bounded lifecycle proof, not broad instrument-discovery
or complete market-history coverage. See the
[B3 acceptance report](acceptance/2026-08-22-b3-instruments-security-master.md) and
[historical identity/listing publication report](acceptance/2026-08-23-historical-identity-listing-publication.md).
The separate B3 listed-company endpoint now has a source-native corporate-action
evidence adapter and live acceptance, but that endpoint remains raw-only because it
does not expose a provider event ID, earliest public publication time, or correction
revision. The public UP2DATA sample has a separate transport-agnostic lifecycle
parser and bounded canonical installation replay with event/control IDs, source
dates, action states, and corrections. Unknown states remain unsupported; no product
transport or production access dependency was added. See the
[listed-company evidence report](acceptance/2026-08-23-b3-corporate-actions-evidence.md),
[UP2DATA sample report](acceptance/2026-08-23-b3-up2data-corporate-actions-evidence.md),
and [corporate-action publication report](acceptance/2026-08-24-corporate-action-publication.md).
The official B3 and NYSE calendar/hour pages now have raw-first adapters and a
canonical calendar compiler/publisher. A live bounded acceptance published one
append-only manifest plus an explicit row for every covered date: five BVMF sessions
for 2026-08-24 through 2026-08-28 and all 365 XNYS dates for 2026. Pinned
after-close lookups crossed a normal BVMF close and the XNYS Thanksgiving closure.
This remains current/reference evidence available only from local receipt time;
neither source exposes the historical publication/correction chronology required
to close the v0.2 calendar gate. See the
[source evidence report](acceptance/2026-08-23-b3-market-calendar-evidence.md) and
[canonical publication report](acceptance/2026-08-23-exchange-calendar-publication.md).

## Current continuation boundary

- Repository: `/home/luis/dev/invs`
- Branch: `main`
- v0.3 status: complete; see [the v0.3 acceptance report](acceptance/2026-08-29-v0.3-feature-platform.md)
- v0.4 status: complete; see [the v0.4 acceptance report](acceptance/2026-08-29-v0.4-hypothesis-loop.md)
- v0.5 status: complete; see [the v0.5 acceptance report](acceptance/2026-08-29-v0.5-backtesting.md)
- v0.6 status: complete; see [the v0.6 acceptance report](acceptance/2026-08-29-v0.6-paper-trading.md)
- v1.0 status: in progress; see the [resilience acceptance report](acceptance/2026-08-30-v1-resilience.md)
- v1.0 current host readiness: accepted snapshot; see the [operational readiness note](acceptance/2026-08-30-v1-live-operations.md)
- v1.0 installation lifecycle: accepted repository-side; see the [install/upgrade acceptance report](acceptance/2026-08-30-v1-install-upgrade.md)
- v1.0 workflow status: attention by design; see the [workflow integration report](acceptance/2026-08-30-v1-workflow-integration.md)
- Latest v0.4 acceptance boundary: `3ac61e1` (`test(acceptance): prove v0.4 hypothesis loop`)
- Latest v0.5 implementation boundary: `180d5c9` (`fix(backtest): order metric attribution deterministically`)
- Latest v0.6 implementation boundary: `f02a57f` (`feat(observability): add paper portfolio dashboard`)
- Latest v0.6 validation boundary: `95a79b5` (`fix(test): await fresh postgres initialization`)
- Latest v1 compatibility boundary: `f8abe40` (`feat(release): add fail-closed v1 compatibility contract`)
- Latest v1 daily-cycle boundary: `31e34b8` (`feat(operations): orchestrate the local daily research cycle`)
- Latest v1 workflow boundary: `d5b9c22` (`feat(research): add integrated workflow reports`)
- Latest v1 security/recovery boundary: `586d79e` (`feat(operations): enforce security and recovery checks`)
- Latest v1 resilience/bias boundary: `9a0f558` (`test(acceptance): prove v1 recovery and bias scenarios`)
- Latest v1 daily-cycle recovery acceptance boundary: `3f96623` (`test(acceptance): exercise daily-cycle CLI recovery`)
- Latest v1 daily-cycle preflight boundary: `a4f8868` (`fix(operations): validate daily-cycle paper specs`)
- Latest v1 daily-cycle input-binding boundary: `e1ecd4e` (`fix(operations): bind daily-cycle resume to inputs`)
- Latest v1 forward-report-time boundary: `b5200fa` (`fix(acceptance): bind forward evidence to report time`)
- Latest v1 paper replay-compatibility boundary: `59c435f` (`test(paper): preserve legacy report compatibility`)
- Latest v1 operator timing documentation: `7058d10` (`docs(v1): document forward report timing`)
- Latest v1 report-time acceptance coverage: `c12074a` (`test(acceptance): cover forward report time bounds`)
- Latest v1 forward-record contract boundary: `e272e3d` (`feat(acceptance): bind genuine forward paper evidence`)
- Latest v1 forward-record integration correction: `b471908` (`fix(acceptance): isolate forward validation imports`)
- Latest v1 forward-record validation hardening: `be16303` (`fix(acceptance): replay ledger for forward evidence`)
- Latest v1 forward-record acceptance guard: `cf37708` (`test(acceptance): reject replay-only forward records`)
- Latest v1 installation lifecycle acceptance boundary: `c011ab6` (`test(acceptance): add isolated v1 install upgrade drill`)
- Latest v1 workflow evidence isolation boundary: `97e9029` (`fix(acceptance): isolate workflow evidence variants`)
- Latest v1 paper price-basis boundary: `7153626` (`test(paper): reject mixed price bases`)
- Latest v1 paper acceptance-report boundary: `b450f9f` (`feat(paper): derive ledger acceptance reports`)
- Latest v1 paper compatibility hash: `c35ee45` (`chore(release): refresh paper compatibility hash`)
- Latest v1 evidence synchronization boundary: `84c29dd` (`docs(v1): refresh resumability acceptance evidence`)
- Latest v0.4 deterministic read-model fix: `0b97702` (`fix(metadata): keep research snapshots deterministic`)
- v0.4 operator path: `make research-acceptance`
- v0.5 operator paths: `make backtest-acceptance` and `make backtest-reproduction`
- v0.6 operator paths: `make paper-acceptance` and `make paper-reproduction`
- Latest v0.4 read/report boundary: `b6d6bd3` (`feat(metadata): add point-in-time research read models`)
- Latest v0.4 theme fixture boundary: `161a23e` (`feat(theme): add reviewed ai infrastructure reference`)
- Latest v0.3 implementation boundary: `c126d39` (`feat(features): support multi-dataset feature batches`)
- Latest v0.3 reporting boundary: `0454d0e` (`fix(reporting): apply decision clocks to lineage analysis`)
- Latest v0.3 acceptance boundary: `5fc3783` (`test(acceptance): prove v0.3 multi-asset replay`)
- Latest market-momentum contract boundary: `dd450b3` (`feat(registry): register market momentum feature set`)
- Latest market-momentum producer boundary: `941c6b5` (`feat(features): add market momentum producer`)
- Market-momentum implementation acceptance: [bounded Decimal producer, batch, and CLI checks](acceptance/2026-08-29-market-momentum.md)
- Live-accepted ALFRED implementation boundary: `31378be` (`docs: record ALFRED milestone and roadmap`)
- Latest provider-contract implementation boundary: `8f2680f` (`feat(provider): standardize downloaded resource results`)
- Latest research-visibility implementation boundary: `806874a` (`feat(research): inspect filings and feature artifacts`)
- Latest v0.1 operations implementation boundary: `63d479d` (`fix(operations): clear superseded source alerts`)
- Latest roadmap discovery boundary: `0be506c` (`docs(roadmap): record Yahoo B3 market-data bridge discovery`)
- Latest v0.2 contract/fixture boundary: `4d483ac` (`feat(data): add historical truth contracts and fixtures`)
- Latest v0.2 durable metadata boundary: `d963180` (`feat(data): publish historical truth metadata boundary`)
- Latest bounded B3 instrument boundary: `c43204f` (`feat(data): add bounded B3 instrument source`)
- B3 live source evidence: [bounded InstrumentsConsolidated acceptance](acceptance/2026-08-22-b3-instruments-security-master.md)
- Latest B3 listed-company corporate-action boundary: `0ad46c8` (`feat(provider): add bounded B3 corporate-action evidence`)
- Latest B3 UP2DATA lifecycle parser boundary: `8b9916f` (`feat(provider): parse B3 UP2DATA corporate-action lifecycle`)
- B3 UP2DATA sample acceptance: [source-native lifecycle evidence](acceptance/2026-08-23-b3-up2data-corporate-actions-evidence.md); production access remains blocked, while the later bounded path publishes only installation-replay revisions with unsupported states
- Latest B3 market-calendar evidence boundary: `6f61a84` (`feat(provider): parse B3 market-calendar evidence`)
- Latest exchange-calendar provider boundary: `acd8eec` (`feat(provider): parse official exchange calendars`)
- Latest exchange-calendar publication boundary: `77fee79` (`feat(data): publish versioned exchange calendars`)
- Latest historical-calendar provider boundary: `beba0a1` (`feat(provider): verify historical calendar artifacts`)
- Latest historical-calendar publication boundary: `173023e` (`feat(data): publish historical calendar artifacts`)
- Latest calendar-pin research boundary: `d7f4e18` (`feat(research): pin calendar decision clocks`)
- Latest Nasdaq historical-hours admission fix: `6193dd0` (`fix(provider): admit Nasdaq historical session hours`)
- Historical calendar acceptance: [bounded XNAS/BVMF artifact chronology and decision clocks](acceptance/2026-08-24-historical-calendar-publication.md)
- Latest corporate-action schema/publication boundary: `8a318f0` (`feat(data): publish corporate action versions`)
- Latest adjustment-artifact boundary: `7655c74` (`feat(research): publish deterministic price adjustments`)
- Latest exact-action provider/evidence boundaries: `33d8658` and `435dc51`
- Latest price-basis safety boundaries: `4a3af81` and `e3ccbee`
- Latest supported action-snapshot boundary: `9c8ff81` (`feat(research): export as-of action snapshots`)
- Latest canonical action-hash boundary: `4751235` (`fix(research): canonicalize action record hashes`)
- Corporate-action acceptance: [bounded SEC publication, B3 installation replay, and immutable adjustments](acceptance/2026-08-24-corporate-action-publication.md)
- Latest PTAX provider/publication boundaries: `486d12d`, `8759780`, and `8b97a59`
- Latest pinned FX research boundary: `cbc0563` (`feat(research): publish pinned PTAX conversions`)
- PTAX acceptance: [bounded official USD/BRL bulletins and pinned conversions](acceptance/2026-08-24-ptax-fx.md)
- Latest SEC filing provider/publication boundaries: `a6d3c17`, `84c09f5`,
  `f07d3a9`, `39db6d0`, and `5347462`
- SEC filing acceptance: [bounded accession identity and exact acceptance-time selection](acceptance/2026-08-24-sec-filing-publication.md)
- Latest B3 COTAHIST provider/publication boundaries: `47c01de`, `317be53`, and `167ce2a`
- B3 price-bridge acceptance: [bounded official raw PETZ3 installation replay](acceptance/2026-08-24-b3-cotahist-price-bridge.md)
- Latest point-in-time audit boundaries: `1c833ee`, `08a05ae`, and `0bfdc27`
- v0.2 acceptance: [bounded US/Brazil bias audit with explicit fitness labels](acceptance/2026-08-24-v0.2-point-in-time-bias-audit.md)
- Latest index-membership provider boundary: `629e82b` (`feat(provider): parse official index membership notices`)
- Latest index-membership publication boundary: `376d0e0` (`feat(data): publish historical universe memberships`)
- Latest mixed-universe identity boundary: `9f488a9` (`fix(metadata): support issuers without SEC identifiers`)
- Latest correction-resolution boundary: `6331d64` (`fix(data): apply corrections before validity filters`)
- Latest B3 lifecycle provider boundary: `030b506` (`feat(provider): parse B3 listing lifecycle notices`)
- Latest index-backed identity boundary: `54c0369` (`feat(data): publish index-backed historical listings`)
- Latest B3 lifecycle publication boundary: `19d8115` (`feat(data): publish B3 listing lifecycle corrections`)
- Historical identity/listing/membership acceptance:
  [bounded INSM/PETZ3 source-backed publication](acceptance/2026-08-23-historical-identity-listing-publication.md);
  final combined bias audit accepted
- ALFRED credentials remain environment-only; do not put them in YAML, run metadata,
  raw attributes, logs, or acceptance artifacts.
- The older `742e5ae` implementation point below remains useful as the exact original
  handoff baseline, but it is no longer the current repository boundary.

## v0.2 final validation

The final exit ladder passed on 2026-08-24:

- `make test`: all Go tests and vet, 19 JSON Schemas, 85 Python tests, and Ruff;
- `make historical-truth-db-test`: apply/replay, append-only constraints,
  transactional rollback, and fresh-image migration wiring;
- `make notebook` and `make dashboard-smoke`;
- `make migrate` against the normal PostgreSQL service;
- `make reconcile`: `issues=0` after acceptance-only identity run manifests were
  moved intact to the retained historical-identity archive; and
- `make health`: PostgreSQL, Jupyter, and Grafana healthy.

The final bias-audit artifact is
`a992011c-80ab-56ce-b851-6f5f1ce46705`, with manifest SHA-256
`06de346b152c60fe9590552365ff333f7599cabbb3b54a0e892a34f8453ab5e2`.
Its exact replay and independent validation both passed.

## Historical implementation handoff point

- Repository: `/home/luis/dev/invs`
- Branch: `main`
- Implementation HEAD: `742e5ae48bd0ad05c496536b6aecf4d5e9dfd241`
- Implementation HEAD subject: `feat(features): publish deterministic market artifacts`
- The two documentation commits that add this handoff and the usage guide follow
  that implementation boundary; the code state they describe is the exact commit
  above.
- Handoff date: 2026-08-12 (local runtime observations may cross UTC midnight)
- At the implementation handoff, no tracked or unrelated implementation changes
  were present; this handoff and the separately requested usage guide were the only
  pending documentation outputs.
- PostgreSQL, Jupyter, and Grafana containers: running and healthy at the time of inspection
- This document is intentionally a separate documentation slice. It must be reviewed and committed by the orchestrator; the document-writing worker must not commit it.

The original handoff described two acceptance boundaries:

1. The original post-metadata v0 acceptance for SEC, Yahoo, FRED, and BCB passed
   at `9ce22d0`. Its recoverable evidence is retained at
   `/home/luis/invs-acceptance/2026-08-12-v0-r3`.
2. CVM integration and the deterministic feature engine advanced beyond that
   acceptance. The fresh post-fallback CVM acceptance against implementation HEAD
   `742e5ae` passed; its external evidence archive and exact checks are recorded in
   [Current CVM live evidence status](#current-cvm-live-evidence-status) below. The
   acceptance report is not a repository file, but the replay itself is complete and
   reviewable from the retained archive.

The local bind-mounted checkout currently contains valid manifest-backed Yahoo and
FRED/ALFRED output plus raw evidence and two inspected `market-basic` artifacts.
The CVM live-run evidence was written to separate acceptance archives rather than
to this checkout. The feature artifacts remain replaceable derived outputs; the
recovery backup includes them and validates their selected normalized lineage.

## Mission and architectural boundaries

The platform is a small self-hosted personal quant-research foundation. Its job is
to collect vendor data with explicit knowledge timestamps, preserve the original
bytes, publish validated canonical analytical data, and make point-in-time research
reproducible. It is not yet a trading system.

The durable truth flow is:

```text
provider response
      |
      v
immutable raw bytes + hash + run manifest
      |
      v
validated canonical model
      |
      v
content-named Parquet part + atomic manifest
      |
      +--> DuckDB / Python research / deterministic features
      |
      +--> PostgreSQL latest-only operational projection --> Grafana
```

The boundaries are deliberate:

| Boundary | Owns | Must not become |
| --- | --- | --- |
| Collector | source scheduling, request policy, raw-first ordering, run lifecycle, orchestration | a research or feature-calculation engine |
| Provider adapter | vendor URLs, response parsing, source-specific timestamps and locators | the canonical storage layout |
| Raw store | immutable bytes, metadata, hashes, recovery reads | a vendor parser or a mutable cache |
| Validator/normalizer | semantic validation and vendor-neutral records | network access or operational leases |
| Canonical Parquet | authoritative historical analytical records and manifests | a latest-only dashboard cache |
| PostgreSQL | identities, versioned identifiers, source catalog, run state, run inputs, replaceable projections | bulk canonical history |
| DuckDB/Python | manifest-backed queries, explicit point-in-time joins, deterministic derived artifacts | direct vendor calls |
| Grafana | operational health, current projections, visible coverage gaps | synthetic observations or historical research |

Raw evidence and canonical Parquet are authoritative. PostgreSQL projections may
be rebuilt or replaced. A dashboard value must never be treated as the historical
dataset merely because it is convenient to query.

Primary design references are [README.md](../README.md),
[architecture.md](architecture.md), and [ADR 0001](adr/0001-storage-boundaries.md)
through [ADR 0007](adr/0007-versioned-calendars-and-decision-clocks.md).

## Completed commit sequence

The branch is linear. These are the commits present before this handoff document
and the separate usage guide were committed:

```text
851fbd4 chore: initialize local development workspace
e9ff43c docs: define point-in-time data architecture
b48c114 feat: add idempotent market data collectors
561d333 feat: add local research and observability runtime
3299471 feat: add reproducible research operations
2bd1b97 feat: persist trustworthy ingestion metadata
e2168b8 feat(storage): add latest observation projections
2dd7cfb feat(data): enforce canonical parquet v1
a2cc94b feat(research): support canonical parquet v1
0e72bad feat(observability): add latest snapshot dashboard
b0f10c1 feat(collector): stamp canonical provenance
fe6d6c5 feat(collector): publish monotonic operational snapshots
8b08f84 fix(metadata): bound snapshot finalization batches
cb943cc fix(provider): preserve SEC partial raw evidence
764b490 test(observability): smoke all Grafana dashboards
cfa31b1 docs: align publication and recovery boundaries
60b4e61 feat(storage): publish canonical data through manifests
abfa909 fix(data): align macro snapshot revisions
30c4afe fix(runtime): propagate collector Git provenance
e2160a8 feat(storage): persist raw run manifests
7cd98b1 fix(data): make identical prices idempotent
723f612 feat(operations): add orphan run cancellation
15521ce fix(data): reject sub-microsecond timestamps
d5b9ed2 fix(research): align point-in-time availability semantics
cc2f22b fix(provider): mark FRED release precision unknown
825c4c3 docs(schema): define observed time precision
e5a640b feat(research): support observed time precision
19010f3 feat(data): preserve observed time precision
4bcaef4 feat(storage): add observed precision snapshots
3abc3d2 feat(provider): add BCB SGS adapter
a590eb5 docs: clarify pre-contract data terminology
4680cdb feat(collector): integrate BCB SGS ingestion
983560b feat(observability): expose BCB snapshots
1326aba fix(collector): align receipt timestamps
659b987 fix(data): preserve SEC filing identities
9ce22d0 feat(metadata): persist effective run inputs
573ac75 docs: record v0 acceptance
1da8541 feat(config): add CVM source catalog
4ede8ce feat(data): add canonical filing metadata
153c568 feat(provider): add CVM source adapter
715c084 docs: define staged CVM boundary
d298cb0 feat(research): expose filing catalog
4282431 feat(collector): integrate CVM filings
092361e docs: record CVM integration boundary
5e0a7d6 fix(provider): tolerate CVM quote defects
8c2015e feat(research): expose point-in-time inputs
38adddc feat(schema): define deterministic feature artifacts
9b8dad0 fix(provider): retain CVM filings with URL identity
e81684e fix(collector): ignore unconfigured CVM issuers
742e5ae feat(features): publish deterministic market artifacts
```

The last three implementation commits in that historical list are the post-contract
CVM/feature additions: `9b8dad0` adds the blank-protocol URL identity fallback,
`e81684e` stops the global CVM archive from turning unconfigured issuers into
rejects, and `742e5ae` adds the bounded `market-basic` engine. Subsequent continuation
commits are recorded in the current continuation boundary above; the accepted
historical metadata slice is `d963180`.

## What is completed

### Identity, configuration, and run lifecycle

The committed configuration and metadata path provides:

- Stable UUID-based issuers, securities, source rows, and ingestion runs.
- Versioned security identifiers with validity ranges and exclusion constraints.
- Exact configured security-to-issuer mappings for the current research universe.
- Open-vocabulary source kinds, so adding a provider does not require a database enum migration.
- Effective run-input metadata, including a canonical JSON SHA-256, provider settings,
  configured entities, source vintage, and requested resources.
- A logical `(data_source_id, run_key)` idempotency key.
- Successful-run retry skipping.
- Rejection of active-key reuse and reuse of keys that ended `partial`, `failed`, or
  `cancelled`.
- Terminal statuses `succeeded`, `partial`, `failed`, and `cancelled`, with received,
  written, rejected, raw-object, raw-byte, cursor, error, and duration metadata.
- Explicit operator cancellation for a confirmed orphan queued/running run. There is
  no automatic timeout cancellation.

Canonical collection requires PostgreSQL. `SyncCatalog` creates or updates source,
issuer, security, and identifier records first. `StartRun` returns both the stable
run UUID and its `data_source_id`; production code does not invent lineage IDs that
are absent from the catalog. Isolated tests may inject UUID fixtures.

### Historical identity and calendar metadata

The v0.2 metadata boundary in
[internal/metadata/historical.go](../internal/metadata/historical.go) publishes an
atomic `HistoricalTruthBatch` containing source-backed identifier, listing,
universe-membership, calendar-manifest, and trading-session records. It validates
half-open validity intervals, source/knowledge ordering, timezone/session shape, and
deterministic calendar fingerprints. Replays with the same ID and canonical
`record_hash` are no-ops; the same ID with different content is an error. Queries
require an explicit `data_source_id`, `as_of`, and `decision_at` and fail closed on
equal-ranked conflicting results.

Migration `000006_historical_truth` stores these records append-only. PostgreSQL
exclusion constraints reject overlapping intervals within one source and revision
while allowing later revisions to overlap, and triggers reject mutation/deletion.
The `scripts/test-historical-truth-db.sh` harness proves migration idempotence,
overlap/revision behavior, exact availability and half-open boundaries, session
close semantics, transaction rollback, fresh initialization, and down/up replay.
This boundary does not yet collect or admit a live historical security-master or
calendar source, and the current YAML universe remains current configuration only.

### Raw evidence and recovery

The file raw store in [internal/storage/raw.go](../internal/storage/raw.go) is
immutable by logical key:

- The stored bytes are hashed with SHA-256 and metadata records source, content type,
  fetch time, size, and attributes.
- Reusing a key with different bytes is an immutable conflict.
- Every run publishes a version-1 raw manifest under
  `runs/<source>/<ingestion-run-id>/manifest.json`.
- Raw manifest entries contain logical key, RawStore object key, actual hash, size,
  content type, fetch time, and sorted attributes. They never depend on a local
  filesystem path.
- Manifest bytes are canonical JSON with a trailing newline; the exact bytes are
  hashed and the hash is stored on `ingestion_runs`.
- `LoadAndVerifyRawManifest` reads every listed object back through the RawStore and
  checks both bytes and metadata.

Collector orchestration stores raw bytes before parsing/normalizing. After storage it
checks the adapter-reported hash against the durable stored hash. Only then does it
stamp accepted rows with source UUID, run UUID, raw hash, raw record locator, and the
aligned ingestion timestamp.

The provider recovery contract is now uniform: each adapter returns every response
body downloaded before a transport or parse/schema error in its common resource
collection. CVM and SEC retain their source-specific metadata through that same
collection, while legacy compatibility fields remain for local callers.

### Reconciliation and restore operations

The current operations boundary is [docs/operations-recovery.md](operations-recovery.md).
`make reconcile` is read-only and verifies active runs, raw manifests/objects,
normalized manifest-listed parts, unlisted Parquet, and feature input lineage.
`make backup` and `make restore` use explicit new destinations and hash-check each
immutable file. The clean-root drill at `0f73e39` restored PostgreSQL to a new
`restore_*` database and returned zero reconciliation findings; it did not claim
the remaining full v0.1 acceptance gate.
The serialized daily wrapper and local operational status check landed in
`68d7fd8`; the append-only canonical replay fix landed in `f92d5a5`. After a
reviewed Yahoo historical correction, the post-commit daily run at `63d479d`
completed with all enabled sources succeeded, zero reconciliation findings, and
`operational_status=ok`. The full current-code evidence is recorded in
[the v0.1 acceptance report](acceptance/2026-08-13-v0.1-foundation.md) and the
[operations runbook](operations-recovery.md).

### Canonical Parquet and manifests

The Go model and writer in [internal/model/model.go](../internal/model/model.go),
[internal/normalize/parquet.go](../internal/normalize/parquet.go), and
[internal/normalize/manifest.go](../internal/normalize/manifest.go) enforce
canonical schema `1.0.0`.

Important invariants:

- Exact decimal lexemes are stored as canonical UTF-8 strings. Scientific notation,
  malformed decimal forms, and ingestion-time rounding are rejected.
- Prices are non-negative and obey OHLC ordering. Volumes are non-negative.
- Temporal order is validated: observed, published, available, ingested, with source
  precision explicitly represented as `date`, `second`, or `unknown`.
- Fundamental period ordering is validated.
- Published rows require source UUID, run UUID, raw SHA-256, record locator,
  ingestion time, and normalizer version.
- Top-level and provenance raw hashes must agree.
- A changed canonical value or identity for the same natural key is a conflict.
  Re-fetching an equivalent canonical row from a changed response envelope is a
  no-op and preserves the first row's raw lineage; the new raw run evidence is
  still retained by its run manifest.
- Fundamental natural keys include taxonomy and unit.
- Macro revisions preserve an A -> B -> A history rather than collapsing by value.
- Existing files are checked for physical schema, supported version, row invariants,
  duplicate natural keys, partition identity, manifest row counts, content hashes,
  and part hashes.
- Parts are immutable and content-named as `part-<sha256>.parquet`.
- `manifest.json` is the only committed reader pointer. Readers do not glob
  `data.parquet`, unlisted parts, or arbitrary recursive Parquet files.
- Publication writes and fsyncs a part, then atomically publishes the manifest.
- A pre-v1 or pre-manifest file fails closed with an actionable migration/reset error;
  it is not overwritten or silently upgraded.

Earlier valid v1 parts may omit the later optional `observed_precision` physical
column. Readers interpret that omission as `unknown` only when the part is already
listed by a valid manifest. This is not permission to accept unmanaged files.

The terms **pre-contract** and **pre-manifest** mean an unmanaged artifact from this
same in-progress first version that was created before the current storage contract
was completed. They do **not** mean a deprecated production version. If such a file
is encountered, the safe action is to archive the complete normalized tree in a
recoverable location, recreate an empty normalized root, preserve raw evidence and
the PostgreSQL catalog, and reingest. Missing provenance must not be invented by an
audited-looking in-place rewrite unless a separate evidence-backed decision proves it
can be reconstructed.

### PostgreSQL operational metadata and projections

The migrations in [migrations](../migrations) provide:

- Core security master, identifiers, data sources, and ingestion runs.
- `market_price_snapshots`, a latest-only price projection.
- `macro_observation_snapshots`, a latest-only current-vintage macro projection.
- Observed-time precision columns.
- Run-input metadata validation.

Projection rows carry source/run foreign keys, exact decimal text, raw hashes,
timestamps, precision, UUID, revision, vintage, and OHLC constraints. Finalization
collapses candidates in memory using the same total order as SQL, then writes accepted
price/macro projections and the terminal run metadata in one PostgreSQL transaction.
The ordering is:

- Price: `observed_at`, then `available_at`, then `ingested_at`, then raw hash.
- Macro: `observed_at`, then `revision`, then `available_at`, then `ingested_at`,
  then raw hash.

Older candidates cannot replace newer projections. A partial multi-entity run can
publish successful entities while an entity with a parse or storage error publishes
no snapshot. Filings do not enter these snapshot tables.

### Research and point-in-time semantics

[python/research/catalog.py](../python/research/catalog.py) registers:

- `prices_canonical`
- `fundamentals_canonical`
- `macroeconomics_canonical`
- `filings_canonical`

The lossless canonical views preserve exact value strings, flags, timestamps, and
provenance. The shorter research views add `DECIMAL(38,18)` and explicitly lossy
`DOUBLE` analysis projections; exact `*_value` or `value_text` columns remain
available.

The catalog fails closed for missing/legacy schema versions, unsupported versions,
missing fields, invalid manifests, hash/row-count mismatches, numeric physical
columns where canonical strings are required, malformed decimal strings, invalid
partition identity, duplicate JSON keys, and unlisted parts.

`research_snapshot(decision_at=..., macro_source=...)` performs explicit as-of selection. It requires
both `available_at <= decision_at` and `observed_at <= decision_at`, applies the
configured security-to-issuer mapping, and prevents accidental cross-issuer joins.
The YAML universe mapping is current configuration, not a historically versioned
identifier-resolution system.

`ResearchCatalog.point_in_time_inputs(...)` is the input boundary for derived
features. It selects manifest-backed price rows for one security at an explicit
decision timestamp and retains close, high, low, volume, availability, raw hash,
manifest path/hash, and part lineage. It does not use latest-only PostgreSQL
snapshots or silently forward-fill.

`filings_as_of(...)` is intentionally separate. Its `historical` mode is only
defensible for datasets with historical public-availability semantics; CVM IPE's
`installation_replay`/`known_at_installation` mode means the installation knew the
row after it collected the source resource. It is not a historical public filing
availability claim.

### Observability

Grafana dashboards are provisioned from
[docker/grafana/dashboards](../docker/grafana/dashboards):

- Pipeline health shows run status, failures, partials, rejected counts, raw bytes,
  and recent activity.
- The market dashboard lists configured securities even when no snapshot exists,
  shows explicit no-snapshot states, and displays only accepted latest Yahoo/FRED/ALFRED/BCB
  projections.
- SEC is labeled ingestion-only because no fundamental snapshot table exists.
- CVM filings are not presented as market snapshots.
- Missing observations are never replaced with zeros or synthetic values.

[python/research/dashboard_smoke.py](../python/research/dashboard_smoke.py) rejects
duplicate JSON keys and emits PostgreSQL `EXPLAIN` statements for all dashboard SQL.

## Provider coverage

### Yahoo daily prices

- Source code: [internal/providers/yahoo/client.go](../internal/providers/yahoo/client.go)
- Config/command: `providers.prices`, `make ingest SOURCE=prices`
- Supports URL escaping for symbols such as `^BVSP` and `BRK/B`.
- Produces canonical daily OHLCV rows with exact numeric lexemes and provenance.
- Current Yahoo downloads are conservative installation-knowledge data. A row's
  trading date is not treated as proof that the row was knowable on that date.
- The v0 acceptance retained 1,661 normalized price rows for the configured slice.

### SEC company metadata, facts, and filings

- Source code: [internal/providers/sec/client.go](../internal/providers/sec/client.go)
- Config/command: `providers.sec`, `make ingest SOURCE=sec`
- Handles quoted or numeric CIK/SIC forms, company metadata, submissions, and
  company facts.
- Preserves original numeric lexemes and rejects malformed/non-finite values.
- Uses exact filing acceptance timestamps when SEC supplies them; otherwise applies
  the adapter's conservative fallback rather than treating a filed date as an exact
  instant.
- SEC facts become canonical fundamental observations. Submissions separately
  publish accession-keyed canonical filing metadata with exact acceptance-time
  publication/availability, primary-document URLs, source civil dates, and raw
  lineage. Source `reportDate` is not promoted to `observed_at`.
- The v0.2 filing acceptance retained 25,135 normalized SEC facts and 1,001 filings
  from 26,136 received records, zero rejects, and two raw objects. See the
  [acceptance report](acceptance/2026-08-24-sec-filing-publication.md).

### FRED

- Source code: [internal/providers/fred/client.go](../internal/providers/fred/client.go)
- Config/command: `providers.fred`, `make ingest SOURCE=fred`
- Current-vintage series are stored as canonical macro observations with revision and
  vintage fields where available.
- Non-finite values are rejected.
- FRED release precision is treated conservatively as unknown when an exact release
  instant is not supplied.
- A current-vintage pull is not a historical vintage store and cannot prove what was
  knowable on an earlier date.
- The v0 acceptance retained `DGS10` and `CPIAUCSL` output (16,137 and 954 rows in
  the retained r3 acceptance report).

### ALFRED historical vintages

- Source code: [internal/providers/alfred/client.go](../internal/providers/alfred/client.go)
- Config/command: `providers.alfred`, `make ingest SOURCE=alfred`
- The provider is disabled by default and requires environment-only `FRED_API_KEY`.
- Requests use `output_type=1`, the complete supported real-time left boundary,
  an explicit closed right boundary, and paginated JSON raw objects.
- Date-only vintage starts become `published_at`/`vintage_at` with date precision;
  the safe research availability cutoff is 36 hours later.
- Canonical revisions are deterministic ordinals per observation date, including
  equal-value and explicit missing vintages. Historical reruns do not auto-renumber.
- Fixture/unit/integration acceptance and the bounded live CPIAUCSL replay pass.

### BCB SGS

- Source code: [internal/providers/bcb/client.go](../internal/providers/bcb/client.go)
- Config/command: `providers.bcb`, `make ingest SOURCE=bcb`
- Series are configured with code, geography, unit, frequency, seasonal adjustment,
  and optional date bounds.
- Output is canonical macro data with exact strings, source metadata, and provenance.
- Like current FRED data, BCB backfills are current-vintage installation evidence,
  not an historical vintage reconstruction.
- The v0 acceptance retained 2,416 normalized rows.

### CVM IPE and CAD

CVM is integrated behind an explicit staged boundary. Configuration is in
[config/config.go](../config/config.go) and
[config/config.example.yaml](../config/config.example.yaml). The default example
keeps CVM disabled. CVM IPE archives are selected by explicit years; document URLs
are not discovered by crawling.

#### IPE

- The provider stores the IPE metadata response, each requested yearly ZIP, parser
  metadata, and hashes as raw resources before canonical publication.
- Only an exact configured `universe[].cvm_code` mapping selects a row for canonical
  publication. The archive is global, so rows for issuers outside the configured
  universe are expected and are ignored, not counted as malformed rejects. Cursor
  fields expose `ipe_rows_matched`, `ipe_rows_unconfigured`, `ipe_rows_ignored`, and
  `ipe_rows_ambiguous`.
- Duplicate configured CVM codes are an error. A source row cannot be guessed into
  an issuer by name or legal-entity text.
- Canonical rows use `source=cvm_ipe` and the filing schema. The source delivery date
  is retained as `filing_date`; it is not silently promoted to a public publication
  instant.
- `published_at` is null with `published_precision=unknown` when the source does not
  provide a defensible publication instant. `available_at` is durable receipt time,
  so the result supports installation replay only. A reference/period date may fill
  `period_end` or `observed_at`, but never `available_at`.

#### Blank protocol identity fallback

The official IPE archive contains valid rows whose `Protocolo_Entrega` field is
blank. Those rows are retained when and only when the download URL contains numeric
`numProtocolo`, `numSequencia`, and `numVersao` parameters and the URL version agrees
with the source version field.

For these rows:

- The original blank `Protocol`/`AccessionNumber` is preserved as blank.
- The exact source URL and all source fields remain in the canonical/raw evidence.
- The deterministic `SourceDocumentID` is
  `cvm-ipe:<cvm_code>:urlsha256-<sha256-of-the-exact-URL>:v<version>`.
- Missing or invalid URL identity parameters remain row-level rejects.

This is a defensible identity fallback because it derives identity from the
authoritative document locator without pretending the missing source field existed.
It is not a fabricated protocol number.

The adapter tolerates literal bare quotes found in the official semicolon-delimited
IPE/CAD extracts with the parser's lazy-quote mode, preserves the literal text, and
continues with row-level errors for invalid records. It still enforces the expected
field count and required identity/date/URL fields.

#### CAD

CVM CAD is a current issuer snapshot, not versioned filing history. It is retained as
raw evidence only. There is no canonical CAD filing dataset or latest CAD projection,
and CAD must not be joined into a historical/as-of filing or market snapshot.

When CAD is enabled, the collector records `cad_status=ingestion_only_not_published`
and `cad_rows_not_published`. Those rows are counted in the run's non-published
rejection metric so the run is normally `partial`; this is an explicit boundary, not
an accidental parser failure. A future CAD feature must first define a versioned
issuer-metadata contract rather than quietly treating a current extract as history.

#### Current CVM live evidence status

After the URL-identity and unconfigured-row fixes, the fresh official IPE replay
passed against implementation HEAD `742e5ae`.

Evidence archive:
`/home/luis/invs-acceptance/2026-08-12-cvm-ipe-current-Q8LklS`

- Run `9953e213-bbec-48a4-9151-f58ed9951baf` finished `succeeded`.
- The logical run received 30,232 rows, selected 199 Petrobras-code `9512` rows,
  wrote 199 canonical filings, and rejected 0 rows.
- 29,934 global-archive rows were explicitly ignored as unconfigured; there were
  0 ambiguous mappings.
- Two raw resources were retained, totaling 1,424,682 bytes.
- Eight blank-protocol rows were retained through validated URL identity, and 314
  literal-quote rows were accepted.
- ZIP SHA-256:
  `6b706bc15afc6d420189d38f3d54ae7c079d759811eace27f13b0d1eb8576e12`.
- Raw manifest SHA-256:
  `f05840c862360fcfe110d2d2b313b744dd5cf4cba6398897aed99da4957787b5`.
- Canonical manifest SHA-256:
  `5a09d617d186743716006c7b22bc207076f8576126eb3810ec80ca183affa543`.
- Canonical part SHA-256:
  `330bc135d8790e773b5d7b91478c42204b58b3f44a2c2ac197ad15271eb415e7`.
- DuckDB/Python readback found 199 filing rows, no missing provenance, and all
  eight blank-protocol rows using deterministic URL identities. The exact
  `available_at` boundary was `2026-08-13T00:07:51.508405Z`: one microsecond before
  returned 0 rows, at the boundary returned 199, and one microsecond after returned
  199.
- Retrying the same successful logical key exited successfully and skipped without
  rewriting raw or normalized files.

The CAD canary also passed its parser/raw-preservation check at
`/home/luis/invs-acceptance/2026-08-12-cvm-cad-current-v3rGsD`. It received and
parsed 2,677 rows, retained 1,493,128 raw bytes with SHA-256
`1035da156d0ffe2da8e809ad098387f0d7a88941eee3da77043782f6a4c5a6e7`, and accepted
7 literal-quote rows with 0 shape errors. Its `partial` terminal state is expected:
CAD remains explicitly raw-ingestion-only, so its 2,677 rows were not published as
canonical records. The official CVM acceptance gate is complete; CAD canonical
publication remains intentionally out of scope.

## Deterministic feature artifacts

The feature contract was introduced in [ADR 0005](adr/0005-deterministic-feature-artifacts.md)
and the schemas [feature-observation.schema.json](../schemas/feature-observation.schema.json)
and [feature-manifest.schema.json](../schemas/feature-manifest.schema.json). The
bounded implementation is in [python/research/features.py](../python/research/features.py).

The v0.3 registry boundary is defined by [ADR 0010](adr/0010-versioned-feature-set-registry.md),
the checked-in [feature-set-registry.json](../schemas/feature-set-registry.json), and
its strict resolver in [python/research/registry.py](../python/research/registry.py).
The registry is a closed allowlist keyed by exact feature-set name and version. It
declares required canonical inputs, historical-fitness labels, lookback, calendar
clock, null policy, computation delay, output types, and generator implementation.
The registry document's exact bytes are fingerprinted into later batch identity.

The closed `market-basic` 1.0.0 registry contains exactly:

- `close`: selected daily close;
- `return_1d`: `close_t / close_(t-1) - 1`;
- `range_1d`: high minus low; and
- `volume`: selected volume, or null when unavailable.

The closed `market-momentum` 1.0.0 registry contains exactly:

- `return_1m`, `return_3m`, `return_6m`, and `return_12m`: close-to-close simple
  returns over 21, 63, 126, and 252 eligible observations;
- `realized_volatility_1m`: annualized sample standard deviation of the trailing 21
  one-observation returns; and
- `max_drawdown_1m`: the minimum running close-to-peak return across 21 eligible
  closes.

The strict contracts are
[`feature-momentum-manifest.schema.json`](../schemas/feature-momentum-manifest.schema.json)
and
[`feature-momentum-observation.schema.json`](../schemas/feature-momentum-observation.schema.json).
The maximum family warmup is 253 closes; shorter outputs become available at their
own prerequisites, while missing required closes become typed nulls and invalid or
zero-denominator calculations reject the partition.

The engine exposes `compute_market_basic_features`, `publish_market_basic`,
`build_market_basic_artifact`, `compute_market_momentum_features`,
`publish_market_momentum`, `publish_feature_artifact`, `read_feature_artifact`,
`validate_feature_artifact`, and `compute_input_fingerprint`.

The current engine:

- selects inputs through `ResearchCatalog.point_in_time_inputs`;
- uses `Decimal` calculations and emits exact decimal strings or null, never JSON
  floating-point feature values;
- does not forward-fill missing prerequisites;
- records `decision_at`, maximum selected input availability, computation delay, and
  derived feature `available_at`;
- requires and validates the ADR 0007 calendar pin and
  `after_close_next_session` decision-clock policy;
- fingerprints the canonical input-selection envelope, including the calendar pin
  plus selected manifest and part hashes;
- writes an immutable content-named Parquet part and a manifest;
- derives deterministic artifact identity from the complete input fingerprint and
  detects explicit identity conflicts;
- rejects tampered parts, unknown versions/features, duplicate JSON keys, unlisted
  files, hash mismatches, timing violations, and physical schema drift. The generic
reader dispatches to the strict momentum reader when the manifest declares
`market-momentum` 1.0.0.

The artifact reader follows the feature manifest, not recursive discovery. The
dataset-level [batch manifest](../schemas/feature-batch-manifest.schema.json) and
[python/research/batches.py](../python/research/batches.py) now orchestrate explicit
security/decision partitions by reusing those immutable child artifacts. A batch is
reproducible from its registry hash, universe fingerprint, calendar pin, schedule,
input manifests/parts, and output child hashes. Missing input partitions are recorded
as explicit rejects; a rerun validates and reuses the same child artifacts. The
feature artifact catalog now provides a PostgreSQL discovery and lineage envelope for
validated dataset-level batches. It stores registry/input/universe/calendar metadata,
input-fitness labels, and accepted child pointers, but not feature rows. The
`make feature-report` read path adds catalog-level coverage and lineage inspection,
including per-decision unaccounted partition counts, but does not inspect feature
values or compare catalog rows with the feature root. `make reconcile` remains the
filesystem/hash check, and automatic catalog reconciliation, feature-level null
reporting, broader feature families, and clean-root multi-asset acceptance remain
future work. The engine is intentionally not yet a collector stage, strategy API,
backtester, portfolio constructor, label/training pipeline, or ML model registry.

The bounded engine documentation has been aligned without broadening its scope.

## Operational setup and current use

### Runtime prerequisites

- Docker Engine with Compose v2
- GNU Make
- outbound HTTPS access
- a real descriptive SEC User-Agent with contact email when SEC is enabled

The committed safe starter configuration is
[config/config.example.yaml](../config/config.example.yaml). Yahoo and FRED are
enabled by default; SEC, BCB, and CVM are disabled. `make setup` creates untracked,
private `.env` and `config/config.local.yaml` files and creates `data/raw`,
`data/normalized`, `data/features`, and `data/research`.

### Start and migrate

```sh
make setup
# set SEC_USER_AGENT in .env if SEC will be enabled
# edit config/config.local.yaml
make up
make migrate
make health
make urls
```

Fresh PostgreSQL volumes apply migrations `000001_core_metadata` through
`000004_run_inputs`, `000005_nullable_macro_snapshot_value`, and
`000006_historical_truth`. Existing initialized volumes use the idempotent
`make migrate` target. Run `make historical-truth-db-test` to exercise the
historical migration and append-only boundary against PostgreSQL. Current local
runtime observations were PostgreSQL 17.10, Jupyter healthy,
Grafana healthy on `127.0.0.1:3001`, and PostgreSQL healthy on the default database
port. The configured host Grafana port is environment-dependent; use `make urls`.

### Collect

```sh
make ingest SOURCE=prices
make ingest SOURCE=sec
make ingest SOURCE=fred
make ingest SOURCE=bcb
make ingest SOURCE=cvm
```

The CVM command only publishes IPE rows for exact configured CVM codes. For a useful
CVM run, add the issuer's code to the local universe and choose explicit IPE years.
The default example has no CVM code and leaves CVM disabled.

Use a stable run key to test retry behavior:

```sh
make ingest SOURCE=prices RUN_KEY=my-retry-key
make ingest SOURCE=prices RUN_KEY=my-retry-key
```

The second successful attempt skips without fetching/writing the source again. A
partial or failed key is not reusable; choose a new key. `make rerun` generates and
reuses one key for an immediate retry demonstration.

If an orphan active run is confirmed, cancellation is explicit and reasoned:

```sh
docker compose --profile collect run --rm collector \
  --cancel-run --cancel-source fred \
  --cancel-run-key '<exact-run-key>' \
  --cancel-reason 'operator confirmed orphan after inspection'
```

At handoff time, one old `fred` run (`post-v1-acceptance-20260812/fred`) remained
`running` in PostgreSQL. It was not automatically cancelled; an operator should
inspect and cancel it only if confirmed orphaned.

### Research and feature use

The supported research path is:

1. Run collection and confirm raw manifests and canonical manifests exist.
2. Open or execute [python/notebooks/vertical_slice.ipynb](../python/notebooks/vertical_slice.ipynb).
3. Use `ResearchCatalog.research_snapshot(decision_at=..., macro_source=...)` for an explicit as-of
   price/fundamental/macro view.
4. Use `ResearchCatalog.point_in_time_inputs(...)` as the input boundary for a
   derived feature.
5. Use `publish_market_basic(...)` to create an immutable feature artifact under
   `data/features` and `read_feature_artifact(...)` to validate it later.
6. Use `make feature-catalog BATCH_MANIFEST=/data/features/batches/.../manifest.json`
   to validate and register a completed dataset-level batch in PostgreSQL.
7. Use `make feature-report` to inspect registered batch coverage and lineage in
   text or JSON form without loading feature values into PostgreSQL.
8. Use `make research-theme-snapshot THEME_ID=... DECISION_AT=...` to reconstruct
   the reviewed theme at an explicit cutoff and `make research-status-report
   AS_OF=...` to inspect active hypotheses, reviews, predictions, and outcomes.
9. Use `make research-acceptance` to reproduce the isolated v0.4 theme-backed
   hypothesis loop and its fail-closed database probes.

Run the notebook non-interactively with `make notebook`. It now inspects CVM
filings and an existing feature artifact in separate empty-safe sections; neither
section is joined into the one-to-one price/fundamental/macro snapshot.

### Monitoring

- Grafana is local-only by default. Use the provisioned pipeline-health and market
  dashboards.
- `make dashboard-smoke` validates dashboard JSON and runs PostgreSQL `EXPLAIN` for
  every dashboard query.
- Empty projections are valid and must remain visible as missing state.

## Verification evidence

Checks completed against the historical metadata implementation commit `d963180`:

- `go test ./...` and `go vet ./...` passed, including focused historical metadata
  validation, canonical UTC hashing, and calendar fingerprint tests.
- `make test` passed the Go/schema checks and 58 Python tests with Ruff.
- `python3 schemas/validate_schemas.py` validated 18 JSON Schema documents, and
  `python3 schemas/test_feature_schemas.py` passed the feature fixtures.
- `make notebook` executed the vertical-slice notebook successfully.
- `make dashboard-smoke` validated every dashboard query with PostgreSQL `EXPLAIN`.
- `scripts/test-historical-truth-db.sh` passed forward migration, repeated
  `make migrate`, same-source/same-revision overlap rejection, later-revision
  overlap allowance, exact availability and half-open validity/session boundaries,
  append-only mutation rejection, transaction rollback, fresh-image initialization,
  and migration down/up replay.
- `git diff --check` passed before the implementation commit. The host shell does
  not need a local pytest installation; `make test` installs the Python development
  dependencies inside the Jupyter container.
- Empty-data notebook execution, fixture DuckDB readback, high-precision decimal
  preservation, provenance checks, strict dashboard JSON validation, and image builds
  passed in the earlier v0 acceptance workflow.
- The fresh official CVM IPE acceptance passed at
  `/home/luis/invs-acceptance/2026-08-12-cvm-ipe-current-Q8LklS`, including raw and
  canonical hashes, filing readback, provenance, exact availability-boundary checks,
  and identical-key retry behavior.
- The CAD parser/raw canary passed at
  `/home/luis/invs-acceptance/2026-08-12-cvm-cad-current-v3rGsD`; its partial status
  was the intentional raw-only publication boundary.

The operations slice at `0f73e39` additionally passed `go test ./...`, `go vet ./...`,
the collector image build, `make reconcile` against the running stack, and a
backup/restore drill that loaded PostgreSQL into a new `restore_*` database and
returned zero findings from reconciliation against the restored data root.

The v0.2 contract/fixture slice at `4d483ac` passed `make test`: Go tests and vet,
18 JSON Schema documents, and 58 Python tests including the exact-boundary identity,
membership, calendar, and decision-clock fixtures.

The v0.3 feature-catalog continuation through `12fdf56` additionally passed `make test`
(94 Python tests with Ruff), the full Go test/vet suite, strict schema validation for
21 JSON Schema documents, `docker compose config --quiet`, collector image build,
`make migrate`, and the historical-truth database replay harness. A live temporary
batch published from the real DuckDB catalog was registered twice through the
collector image: the first registration inserted one artifact with one input-fitness
row and one accepted partition, while the second returned `already_present`. The
temporary database row and feature files were removed after the probe.

The read-only catalog-report continuation through `94a3bf0` passed the focused and full
Go test suites, Go vet, strict schema validation for 22 JSON Schema documents,
`git diff --check`, and a rebuilt collector image containing `invs-feature-report`.
The live collector-image probe registered a temporary two-date `market-basic` batch,
confirmed the idempotent second registration, verified complete JSON coverage and
lineage (2/2 partitions and 2 feature rows), exercised text output, and removed all
temporary feature files and catalog child rows afterward.

The `market-momentum` implementation boundary through `941c6b5` passed the focused
registry/producer/batch/CLI suite (15 tests), the full `make test` ladder (100 Python
tests with Ruff), and strict schema validation for 24 JSON Schema documents. The
focused acceptance used manifest-backed daily Parquet with 253 rows, independently
recomputed all six outputs, verified per-output warmup, idempotent publication,
tamper rejection, zero-denominator rejection, direct CLI dispatch, and batch dispatch.
It is an implementation acceptance only: receipt-time price inputs remain
`installation_replay_only`, and no v0.3 exit claim is made.

The corporate-action boundary through `4751235` passed `make test` with 69 Python
tests, `make historical-truth-db-test`, a clean isolated PostgreSQL publication,
exact-key replays, supported as-of snapshot export, immutable adjustment replay, and
an explicit unsupported-action no-output check. Evidence is retained at
`/home/luis/invs-acceptance/2026-08-24-corporate-actions` and summarized in the
[acceptance report](acceptance/2026-08-24-corporate-action-publication.md).

The retained post-metadata v0 r3 acceptance at
`/home/luis/invs-acceptance/2026-08-12-v0-r3` recorded:

| Source | Received | Retained normalized rows | Raw evidence |
| --- | ---: | ---: | ---: |
| SEC | 26,136 | 25,135 fundamentals | 2 objects |
| Yahoo | 1,661 | 1,661 prices | 1 object |
| FRED | 17,811 | 16,137 `DGS10` and 954 `CPIAUCSL` | 2 objects |
| BCB | 2,416 | 2,416 macro rows | 1 object |

The original acceptance verified successful identical-key retries, raw files,
manifest-backed Parquet, DuckDB readback, notebook execution, and populated Grafana
projections for that slice. It did not include CVM or the later feature engine.

## Known limitations and deliberately deferred work

The following are not accidental omissions:

- No full historical point-in-time guarantee for current Yahoo, FRED, or BCB pulls.
  The accepted v0.5 backtest is restricted to explicit `backtest_safe` fixtures and
  hash-pinned artifacts; these current pulls cannot be promoted into that boundary
  without separate historical-publication evidence.
- The bounded ALFRED CPIAUCSL work package and combined v0.2 historical-truth audit
  are accepted. This is still bounded evidence, not broad all-market coverage.
- Historical identity/listing/membership and calendar contracts now have a durable
  PostgreSQL publication/resolution boundary plus synthetic resolver fixtures. The
  bounded B3 public instrument source populates only exact current/reference
  identifier/listing rows. Later bounded live slices publish official Nasdaq-100 and
  Ibovespa add/remove membership revisions, source-backed INSM/PETZ3 initial
  identity/listing intervals, and a PETZ3 trading-cessation correction. Exact
  before/at knowledge and delisting boundaries pass. Exact official XNAS/BVMF
  artifacts additionally close the bounded historical calendar/decision-clock gate.
  This is not a broad security master or complete all-date calendar archive.
- No broad B3/CVM market instrument discovery or complete Brazilian market-data
  path. The bounded official COTAHIST bridge publishes exact PETZ3 raw prices only
  from installation receipt; Yahoo `.SA` remains unadmitted. Broad B3 lifecycle and
  long-history point-in-time availability remain pending, while one bounded official
  PETZ3 identity/listing/membership chain is accepted. B3
  listed-company corporate-action evidence is retained source-natively, but its
  missing publication/revision semantics keep that endpoint raw-only. The B3
  UP2DATA sample revisions are canonical only for installation replay; their unknown
  action state remains unsupported and blocks adjustment.
- No canonical CVM CAD dataset or CAD snapshot table.
- No fundamental or filing latest-only dashboard projection.
- Corporate-action publication and adjustment artifacts are accepted for one exact
  SEC split plus a B3 installation-replay revision family. This is not a broad action
  archive or production B3 delivery claim. Exchange calendars now have both bounded
  BVMF/XNYS current/reference publication and accepted exact-artifact XNAS/BVMF
  historical chronology; dates outside those explicit versions remain unavailable.
- BCB PTAX closing USD/BRL is accepted as the first canonical FX dataset. Five live
  Aug 10-14 bulletins, exact before/at availability selection, content-addressed
  Parquet, exact-key replay, and pinned direct/inverse sell-side conversions are
  retained in the [PTAX acceptance report](acceptance/2026-08-24-ptax-fx.md). This
  does not admit other pairs, carry-forward, midpoint, or triangulation.
- Yahoo chart OHLC and volume are `split_adjusted`. Legacy immutable Yahoo manifests
  carrying the former `raw` label must be archived and reingested; normalization and
  adjustment now fail closed instead of mixing or double-adjusting them.
- Current security-to-issuer mappings are current YAML configuration, not historical
  identity resolution.
- No distributed queue, scheduler, cloud object-store deployment, or production
  multi-user authorization.
- Feature engine v0.3 is complete with a checked-in closed registry and bounded
  `market-basic`, `market-momentum`, `fundamental-growth`, and `macro-state` batch
  runners. A PostgreSQL catalog stores validated dataset-level batch metadata and
  lineage; separate read-only catalog and feature-quality reports expose partition
  coverage, typed nulls, freshness, rejects, source contribution, and raw locators.
  Automatic catalog reconciliation, broader feature coverage, labels, training data,
  and ML behavior remain later boundaries.
- v0.4 is complete at `3ac61e1`: reviewed AI-infrastructure theme context, immutable
  document/text artifacts, human-reviewed event proposals, point-in-time evidence
  packs, memo export/import, append-only hypotheses, frozen predictions, and pinned
  outcomes are accepted. Research files under `data/research` are included in the
  backup/restore path; see the [v0.4 acceptance report](acceptance/2026-08-29-v0.4-hypothesis-loop.md).
- The roadmap is now present; version exit status must be updated there only after
  its stated acceptance gate passes.

- v0.6 is complete at `95a79b5`; its local ledger, paper CLI, PostgreSQL catalog,
  recovery acceptance, and dashboard are now the current paper-operation boundary.
  The next boundary is v1.0 integration and genuine forward-period accumulation;
  broker/live execution remains post-v1.

## Exact next actions

Follow [the roadmap execution index](roadmap/README.md). v0.1 is accepted at
`63d479d` and v0.2 at `0bfdc27`; v0.3 is accepted at
`5fc3783` with reporting correction `0454d0e`; v0.4 is accepted at `3ac61e1`; v0.5
is accepted at `180d5c9`; and v0.6 is accepted at `95a79b5`. Keep receipt-time
prices installation-replay only. The v1.0 integration and operational-hardening
slices are now recorded above. The next smallest cohesive boundary is a genuine
wall-clock forward paper record followed by final workflow/release acceptance;
rolling calibration and broker behavior remain deferred.
