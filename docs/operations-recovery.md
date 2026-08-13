# Recovery, reconciliation, and daily operations

This runbook covers the durable foundation only. It does not make a historical
truth or trading-system claim. The reconciliation and restore implementation is
accepted at `0f73e39`; v0.1 remains in progress until the full clean-machine
acceptance scenario and an observed unattended schedule pass.

## Reconcile before and after operations

Run the full report from the repository root:

```sh
make reconcile
```

The command mounts the repository `data/` tree into the collector container and
reads PostgreSQL metadata. It is read-only. Exit codes are:

- `0`: no findings;
- `1`: a finding was reported with `--fail-on-issues`;
- `2`: the report could not be configured or executed.

For machine-readable output, run the same binary through Compose:

```sh
docker compose --profile collect run --rm collector \
  reconcile --data-root /data --json --fail-on-issues
```

The report checks:

- queued/running ingestion runs, with an operator-review recommendation only;
- raw run manifests, every listed raw object, and the PostgreSQL manifest hash;
- raw manifests that have no terminal PostgreSQL run;
- normalized manifests, missing or hash-mismatched listed parts, and unlisted
  Parquet files;
- feature-manifest structure, output parts, unlisted artifact files, and each
  selected normalized input manifest/part.

The report never cancels a run, deletes an orphan, or rewrites evidence. If an
active run is confirmed orphaned, use the existing explicit collector command
with an exact identity and reason:

```sh
docker compose --profile collect run --rm collector \
  --cancel-run --cancel-source fred \
  --cancel-run-key '<exact-run-key>' \
  --cancel-reason 'operator confirmed orphan after inspection'
```

`--filesystem-only` is useful while inspecting an immutable restore before its
metadata database is available:

```sh
docker compose --profile collect run --rm collector \
  reconcile --data-root /data --filesystem-only --json
```

Raw evidence copied without its PostgreSQL metadata will intentionally appear as
unpaired in this mode. A restored database is required for the zero-finding
durable-state check.

## Backup

Back up to a new, not-yet-existing directory. The script refuses to overwrite an
existing destination:

```sh
make backup BACKUP_DIR=/home/luis/invs-backups/$(date -u +%Y%m%dT%H%M%SZ)
```

The backup contains:

- a plain PostgreSQL dump from the running Compose database;
- `immutable/raw/`, `immutable/normalized/`, and `immutable/features/`;
- `backup-manifest.txt` with file sizes and SHA-256 hashes;
- the effective Git commit and a SHA-256 fingerprint of
  `INVS_CONFIG_FILE` (the configuration itself is not copied).

The backup does not source or print `.env` values and does not include database
passwords. Keep the backup directory outside the checkout and apply the host's
separate retention policy to it. Raw and canonical evidence are retained; old
backups, logs, temporary files, and unlisted evidence require explicit operator
review before removal.

## Restore to a clean root

Restoration is intentionally destination-based and non-destructive:

```sh
backup_dir=/home/luis/invs-backups/<backup>
restore_dir=/tmp/invs-restore-$(date -u +%Y%m%dT%H%M%SZ)
restore_db=restore_$(date -u +%Y%m%d%H%M%S)

make restore \
  BACKUP_DIR="$backup_dir" \
  RESTORE_DIR="$restore_dir" \
  RESTORE_DB="$restore_db"
```

The restore script requires an absent filesystem destination, verifies the
backup manifest before copying each file, and verifies every copied hash again.
When `RESTORE_DB` is provided it must begin with `restore_`; the script creates
that new database and never drops or overwrites the configured application
database. The order is:

1. verify and copy immutable raw, normalized, and feature files;
2. create and load the explicitly named restore database;
3. run reconciliation against the restored data root and restored database;
4. run read-only DuckDB/catalog, feature-validation, notebook, and dashboard
   checks for the restored slice.

Set a database URL for the temporary database without printing it in logs:

```sh
export DATABASE_URL='postgres://<local-user>:<local-password>@127.0.0.1:<port>/<restore_db>?sslmode=disable'
go run ./cmd/reconcile \
    --data-root "$restore_dir/data" \
    --database-url "$DATABASE_URL" \
    --json --fail-on-issues
```

For a restored catalog check, mount the restored data read-only into Jupyter and
inspect `ResearchCatalog.status()`; use the restored feature manifest with
`read_feature_artifact()`. Dashboard smoke checks must target the restored
database, not the original application database.

## Daily host schedule

The v0.1 scheduler remains host-level. Do not add Dagster or Prefect for this
boundary. A cron entry should serialize the batch with a host lock, use one
stable date key, preserve the collector's effective run-input metadata, and run
reconciliation even when collection fails. For example, with `flock` and a
repository-local log directory:

```cron
15 02 * * * cd /home/luis/dev/invs && flock -n .runtime/daily.lock sh -c 'run_key="daily-$(date -u +\%F)"; make ingest SOURCE=all RUN_KEY="$run_key"; collection_status=$?; make reconcile; reconcile_status=$?; test "$collection_status" -eq 0 -a "$reconcile_status" -eq 0' >> /home/luis/dev/invs/logs/daily.log 2>&1
```

Create `.runtime/` and `logs/` with operator-owned permissions before enabling
the entry. The log is the local alert surface: inspect it for failed/partial
runs, stale source coverage, reconciliation findings, projection lag, and disk
headroom. A later v0.1 acceptance slice must observe this schedule and record
the result; documentation alone does not check that gate.

## Recovery evidence

At implementation commit `0f73e39`, the following drill passed on the local
Compose stack:

- backup created a new directory containing 41 immutable files and a PostgreSQL
  dump;
- the files were restored to a previously absent temporary root;
- PostgreSQL was restored into a new `restore_*` database;
- `cmd/reconcile` ran against both restored roots and returned zero findings;
- the original application database and checkout data were not overwritten.

This proves the recovery slice. It does not replace the remaining v0.1
acceptance scenario, which still requires the bounded real-source collection,
notebook/catalog/dashboard checks, failure-preservation coverage, and an
observed daily run.
