# v1.0 release compatibility and upgrade guide

`release/compatibility.json` is the fail-closed contract for the v1.0 line. It pins
the supported Go, Python, PostgreSQL, DuckDB/Parquet, Grafana, Docker Compose, and
container-image versions, then hashes the schema and migration catalogs, registries,
research engines, the data-fitness matrix, and build configuration.

Validate the checkout before using it with an existing data volume:

```sh
make release-validate
```

The command runs inside the pinned Jupyter runtime and checks installed Python
versions as well as repository fingerprints. It also verifies that the
release-pinned data-fitness matrix covers every declared catalog dataset, backtest
input kind, feature family, and workflow evidence surface. A changed schema,
migration ordering, dependency, image digest, registry, engine, fitness entry, or
protected binding fails validation.
Do not hand-edit the manifest to accept a mixed release; update it only as part of a
reviewed release commit with new evidence.

## Forward upgrade

Stop collection, feature, paper, and backup writers before changing the checkout.
Keep the current checkout and data root recoverable, then:

1. Inspect `make ops-status` and `make reconcile`; resolve or explicitly record any
   active or unresolved work.
2. Create a new backup outside the repository:

   ```sh
   make backup BACKUP_DIR=/absolute/path/to/invs-backups/<timestamp>
   make backup-validate BACKUP_DIR=/absolute/path/to/invs-backups/<timestamp>
   ```

3. Update the checkout, run `make release-validate`, and review the reported release
   versions and fingerprints.
4. Apply database changes with `make migrate`. Migrations are forward-only and the
   command is safe to re-run against an already upgraded volume.
5. Run `make reconcile`, `make dashboard-smoke`, and `make health` before resuming
   collection or paper activity.

The upgrade path never rewrites immutable raw, canonical, feature, research, or
paper-ledger evidence. PostgreSQL projections and metadata are restored or rebuilt
only through their documented migration and reconciliation paths.

## Recovery and rollback

If the new checkout or migration is not accepted, do not downgrade the live volume
in place. Restore the backup into an absent clean filesystem destination and a newly
named database whose name starts with `restore_`:

```sh
make restore \
  BACKUP_DIR=/absolute/path/to/invs-backups/<timestamp> \
  RESTORE_DIR=/absolute/path/to/clean-restore \
  RESTORE_DB=restore_invs_<timestamp>
```

Then run the restored-root reconciliation and research/paper checks from
[`docs/operations-recovery.md`](operations-recovery.md). The restore script stages
all copies privately, refuses symlinks and existing destinations, validates every
manifest/hash, and never drops or overwrites the application database.

## Evidence ladder

The release evidence should retain command output and the exact commit for each
accepted boundary:

If the local `.env` intentionally contains a non-loopback binding, prefix the
repository validation with `INVS_BIND_ADDRESS=127.0.0.1`; this evaluates the
committed security baseline without modifying the operator's local file.

The maintained `v1-pre-release-acceptance` target composes the repository-side
validation, operational checks, installation lifecycle, resilience/bias,
replay-only forward-record guard, and workflow checks into one reproducible
pre-release run. It does not run a live source refresh, create a genuine forward
record, or claim an accepted v1.0 release; those remain explicit evidence steps.

```sh
INVS_BIND_ADDRESS=127.0.0.1 make v1-pre-release-acceptance
```

`workflow-acceptance` intentionally reports `attention` until the genuine forward
record exists. `v1-forward-record-acceptance` is the negative proof that retained
replay evidence cannot satisfy that gate. `v1-resilience-acceptance` is the local
recovery and bias proof; its current result is recorded in
[`2026-08-30-v1-resilience.md`](acceptance/2026-08-30-v1-resilience.md).
The data-fitness matrix and its fail-closed mutation checks are recorded in
[`2026-08-30-v1-data-fitness.md`](acceptance/2026-08-30-v1-data-fitness.md).
The disposable installation lifecycle proof is recorded in
[`2026-08-30-v1-install-upgrade.md`](acceptance/2026-08-30-v1-install-upgrade.md).
The combined command results and remaining release gates are summarized in the
[v1.0 pre-release evidence index](acceptance/2026-08-30-v1-pre-release-index.md).

After a real paper session, use `make forward-record-capture` with a repository-
relative ledger root, account ID, and output path. It is the only supported way to
produce the `forward_record.status: genuine` evidence reference: the command
requires a recent reconciled report, requires its invocation-time UTC `recorded_at`
to follow the risk check by no more than 24 hours and not exceed the capture time,
and records account, report, and ledger manifest hashes. Retained historical or
installation-replay fixtures remain ineligible; old reports without `recorded_at`
remain valid for replay acceptance but cannot qualify as genuine forward evidence.
Once a genuine record and aggregate paper report exist, run the maintained genuine
workflow path with repository-relative references:

```sh
V1_FORWARD_RECORD=data/research/forward/v1/forward-record.json \
V1_PAPER_REPORT=data/research/forward/v1/paper-report.json \
V1_PAPER_ACCOUNT_IDS="<account-id>" \
make workflow-acceptance
```

The replay form keeps its reports under `data/research/acceptance/v1/`. With
`V1_FORWARD_RECORD`, the harness derives a separate output directory at
`data/research/acceptance/v1/genuine/<record_id>/`, preserving replay evidence and
keeping each captured record's workflow reports together. Set
`V1_WORKFLOW_OUTPUT_ROOT` when an explicitly named repository-relative variant
directory is needed.

The aggregate paper report must contain the supplied account IDs and accepted
paper checks. Generate it from the account ledger with the read-only
`make paper-acceptance-report` target before running the workflow command; do not
hand-edit the acceptance booleans. The cross-market report may retain `attention`
for explicitly non-backtest-safe commodity evidence.
