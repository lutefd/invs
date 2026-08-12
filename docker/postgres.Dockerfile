FROM postgres:17-alpine

# Copy only forward migrations. The upstream entrypoint executes every *.sql in
# /docker-entrypoint-initdb.d, so exposing rollback files here would be unsafe.
COPY --chmod=0444 migrations/000001_core_metadata.up.sql /docker-entrypoint-initdb.d/000001_core_metadata.sql
COPY --chmod=0444 migrations/000002_latest_observation_snapshots.up.sql /docker-entrypoint-initdb.d/000002_latest_observation_snapshots.sql
COPY --chmod=0444 migrations/000003_observed_precision.up.sql /docker-entrypoint-initdb.d/000003_observed_precision.sql
COPY --chmod=0444 migrations/000004_run_inputs.up.sql /docker-entrypoint-initdb.d/000004_run_inputs.sql
