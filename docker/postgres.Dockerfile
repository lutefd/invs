FROM postgres:17-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193

# Copy only forward migrations. The upstream entrypoint executes every *.sql in
# /docker-entrypoint-initdb.d, so exposing rollback files here would be unsafe.
COPY --chmod=0444 migrations/000001_core_metadata.up.sql /docker-entrypoint-initdb.d/000001_core_metadata.sql
COPY --chmod=0444 migrations/000002_latest_observation_snapshots.up.sql /docker-entrypoint-initdb.d/000002_latest_observation_snapshots.sql
COPY --chmod=0444 migrations/000003_observed_precision.up.sql /docker-entrypoint-initdb.d/000003_observed_precision.sql
COPY --chmod=0444 migrations/000004_run_inputs.up.sql /docker-entrypoint-initdb.d/000004_run_inputs.sql
COPY --chmod=0444 migrations/000005_nullable_macro_snapshot_value.up.sql /docker-entrypoint-initdb.d/000005_nullable_macro_snapshot_value.sql
COPY --chmod=0444 migrations/000006_historical_truth.up.sql /docker-entrypoint-initdb.d/000006_historical_truth.sql
COPY --chmod=0444 migrations/000007_corporate_actions.up.sql /docker-entrypoint-initdb.d/000007_corporate_actions.sql
COPY --chmod=0444 migrations/000008_price_basis.up.sql /docker-entrypoint-initdb.d/000008_price_basis.sql
COPY --chmod=0444 migrations/000009_nullable_price_publication.up.sql /docker-entrypoint-initdb.d/000009_nullable_price_publication.sql
COPY --chmod=0444 migrations/000010_feature_artifacts.up.sql /docker-entrypoint-initdb.d/000010_feature_artifacts.sql
COPY --chmod=0444 migrations/000011_feature_artifact_input_fitness.up.sql /docker-entrypoint-initdb.d/000011_feature_artifact_input_fitness.sql
COPY --chmod=0444 migrations/000012_research_workspace.up.sql /docker-entrypoint-initdb.d/000012_research_workspace.sql
COPY --chmod=0444 migrations/000013_research_theme_context.up.sql /docker-entrypoint-initdb.d/000013_research_theme_context.sql
COPY --chmod=0444 migrations/000014_research_hypothesis_review_at.up.sql /docker-entrypoint-initdb.d/000014_research_hypothesis_review_at.sql
COPY --chmod=0444 migrations/000015_backtest_experiments.up.sql /docker-entrypoint-initdb.d/000015_backtest_experiments.sql
COPY --chmod=0444 migrations/000016_paper_accounts.up.sql /docker-entrypoint-initdb.d/000016_paper_accounts.sql
COPY --chmod=0444 migrations/000017_discovery_indexes.up.sql /docker-entrypoint-initdb.d/000017_discovery_indexes.sql
