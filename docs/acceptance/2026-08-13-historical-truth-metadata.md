# Historical truth metadata boundary acceptance

- Date: 2026-08-13
- Implementation commit: `d963180`
- Scope: v0.2 durable metadata publication and resolution boundary
- Status: accepted implementation slice; not a v0.2 exit acceptance

## Accepted boundary

The implementation provides:

- `HistoricalTruthBatch` publication for source-backed security identifiers,
  listings, universe memberships, calendar manifests, and trading sessions;
- validation for schema/version identity, half-open validity intervals,
  `available_at <= recorded_at`, calendar fingerprints, IANA timezones, and open or
  closed session shape;
- explicit-source PostgreSQL as-of resolution for identifiers, listings, universes,
  and trading-session boundaries;
- deterministic canonical record hashes with exact replay acceptance and same-ID
  content conflicts; and
- append-only PostgreSQL tables with same-source/same-revision exclusion constraints,
  immutable record hashes, composite calendar/session foreign keys, mutation guards,
  and safe rollback.

## Evidence

The following passed against the implementation slice:

- `go test ./...`
- `go vet ./...`
- `make test` — 58 Python tests, Go checks, schema checks, and Ruff
- `make notebook`
- `make dashboard-smoke`
- `make historical-truth-db-test`

The PostgreSQL harness exercised repeated `make migrate`, same-source/same-revision
overlap rejection, later-revision overlap allowance, exact availability and
exclusive validity boundaries, append-only mutation rejection, session close
half-open behavior, transaction rollback, fresh-image initialization, and migration
down/up replay. Focused Go tests also cover UTC-equivalent timestamp hashes,
order-independent calendar fingerprints, batch/manifest requirements, and invalid
availability/session states.

## Explicit non-claims

This report does not claim that v0.2 is complete. At the time of this metadata
boundary acceptance, no live security-master or exchange-calendar source had been
admitted and no point-in-time bias audit had passed. The later bounded B3 snapshot
admission is recorded separately in the [B3 acceptance report](2026-08-22-b3-instruments-security-master.md);
the current YAML universe remains current configuration rather than historical
identity evidence outside that explicit B3 mapping.
