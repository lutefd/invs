SHELL := /bin/sh

LOCAL_UID ?= $(shell id -u)
LOCAL_GID ?= $(shell id -g)
INVS_GIT_COMMIT ?= $(shell git rev-parse --verify HEAD 2>/dev/null | tr '[:upper:]' '[:lower:]')
COMPOSE = LOCAL_UID=$(LOCAL_UID) LOCAL_GID=$(LOCAL_GID) INVS_GIT_COMMIT=$(INVS_GIT_COMMIT) docker compose
SOURCE ?= all
RUN_KEY ?=
RUN_KEY_ARG = $(if $(RUN_KEY),--run-key $(RUN_KEY),)
DASHBOARDS := $(wildcard docker/grafana/dashboards/*.json)

.PHONY: setup config up migrate historical-truth-db-test health urls ingest rerun daily ops-status reconcile backup restore feature feature-validate feature-batch feature-batch-validate feature-catalog feature-report action-snapshot adjust adjust-validate bias-audit bias-audit-validate test notebook dashboard-smoke validate down clean

setup:
	@test -f .env || (umask 077 && cp .env.example .env)
	@test -f config/config.local.yaml || (umask 077 && cp config/config.example.yaml config/config.local.yaml)
	@mkdir -p data/raw data/normalized data/features
	@echo "Local files ready. Set a real SEC contact in .env and config/config.local.yaml."

config:
	@$(COMPOSE) config --quiet

up: setup config
	@$(COMPOSE) up -d --build postgres jupyter grafana

migrate: setup config
	@$(COMPOSE) up -d --wait --build postgres
	@$(COMPOSE) exec -T postgres sh -c \
		'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Atc "SELECT to_regclass('"'"'public.market_price_snapshots'"'"'), to_regclass('"'"'public.macro_observation_snapshots'"'"')"' | \
		grep -qx 'market_price_snapshots|macro_observation_snapshots' || \
		$(COMPOSE) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -v ON_ERROR_STOP=1' < migrations/000002_latest_observation_snapshots.up.sql
	@$(COMPOSE) exec -T postgres sh -c \
		'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Atc "SELECT count(*) FROM information_schema.columns WHERE table_schema='"'"'public'"'"' AND column_name='"'"'observed_precision'"'"' AND table_name IN ('"'"'market_price_snapshots'"'"','"'"'macro_observation_snapshots'"'"')"' | \
		grep -qx '2' || \
		$(COMPOSE) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -v ON_ERROR_STOP=1' < migrations/000003_observed_precision.up.sql
	@$(COMPOSE) exec -T postgres sh -c \
		'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Atc "SELECT 1 FROM pg_constraint WHERE conrelid='"'"'public.ingestion_runs'"'"'::regclass AND conname='"'"'ingestion_runs_metadata_run_inputs_check'"'"'"' | \
		grep -qx '1' || \
		$(COMPOSE) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -v ON_ERROR_STOP=1' < migrations/000004_run_inputs.up.sql
	@$(COMPOSE) exec -T postgres sh -c \
		'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Atc "SELECT is_nullable FROM information_schema.columns WHERE table_schema='"'"'public'"'"' AND table_name='"'"'macro_observation_snapshots'"'"' AND column_name='"'"'value'"'"'"' | \
		grep -qx 'YES' || \
		$(COMPOSE) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -v ON_ERROR_STOP=1' < migrations/000005_nullable_macro_snapshot_value.up.sql
	@$(COMPOSE) exec -T postgres sh -c \
		'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Atc "SELECT count(*) FROM pg_class WHERE oid IN (to_regclass('"'"'public.security_identifier_versions'"'"'), to_regclass('"'"'public.security_listing_versions'"'"'), to_regclass('"'"'public.universe_memberships'"'"'), to_regclass('"'"'public.calendar_manifests'"'"'), to_regclass('"'"'public.trading_sessions'"'"'))"' | \
		grep -qx '5' || \
		$(COMPOSE) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -v ON_ERROR_STOP=1' < migrations/000006_historical_truth.up.sql
	@$(COMPOSE) exec -T postgres sh -c \
		'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Atc "SELECT to_regclass('"'"'public.corporate_action_versions'"'"')"' | \
		grep -qx 'corporate_action_versions' || \
		$(COMPOSE) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -v ON_ERROR_STOP=1' < migrations/000007_corporate_actions.up.sql
	@$(COMPOSE) exec -T postgres sh -c \
		'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Atc "SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='"'"'public.market_price_snapshots'"'"'::regclass AND conname='"'"'market_price_snapshots_price_basis_check'"'"'"' | \
		grep -q 'split_adjusted' || \
		$(COMPOSE) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -v ON_ERROR_STOP=1' < migrations/000008_price_basis.up.sql
	@$(COMPOSE) exec -T postgres sh -c \
		'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Atc "SELECT is_nullable FROM information_schema.columns WHERE table_schema='"'"'public'"'"' AND table_name='"'"'market_price_snapshots'"'"' AND column_name='"'"'published_at'"'"'"' | \
		grep -qx 'YES' || \
		$(COMPOSE) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -v ON_ERROR_STOP=1' < migrations/000009_nullable_price_publication.up.sql
	@$(COMPOSE) exec -T postgres sh -c \
		'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Atc "SELECT to_regclass('"'"'public.feature_artifacts'"'"')"' | \
		grep -qx 'feature_artifacts' || \
		$(COMPOSE) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -v ON_ERROR_STOP=1' < migrations/000010_feature_artifacts.up.sql
	@$(COMPOSE) exec -T postgres sh -c \
		'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -Atc "SELECT to_regclass('"'"'public.feature_artifact_input_fitness'"'"')"' | \
		grep -qx 'feature_artifact_input_fitness' || \
		$(COMPOSE) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -v ON_ERROR_STOP=1' < migrations/000011_feature_artifact_input_fitness.up.sql

historical-truth-db-test: config
	@scripts/test-historical-truth-db.sh

health:
	@$(COMPOSE) ps
	@$(COMPOSE) exec -T postgres sh -c 'pg_isready -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"'
	@$(COMPOSE) exec -T jupyter python -c "import socket; socket.create_connection(('0.0.0.0', 8888), 2).close()"
	@$(COMPOSE) exec -T grafana wget -q -O /dev/null http://0.0.0.0:3000/api/health

urls:
	@$(COMPOSE) exec -T jupyter jupyter server list
	@echo "Grafana: http://0.0.0.0:$${GRAFANA_PORT:-3000}"

ingest: setup config
	@$(COMPOSE) --profile collect run --rm collector --source $(SOURCE) $(RUN_KEY_ARG)

rerun:
	@run_key="operator-retry-$(SOURCE)-$$(date +%s)"; \
		$(MAKE) ingest SOURCE=$(SOURCE) RUN_KEY="$$run_key" && \
		$(MAKE) ingest SOURCE=$(SOURCE) RUN_KEY="$$run_key"

daily: setup config
	@scripts/daily.sh "$(DAILY_DATE)"

ops-status: setup config
	@scripts/ops-status.sh

reconcile: setup config
	@$(COMPOSE) --profile collect run --rm collector reconcile --data-root /data --fail-on-issues

backup: setup config
	@test -n "$(BACKUP_DIR)" || (echo "BACKUP_DIR is required" >&2; exit 2)
	@scripts/backup.sh "$(BACKUP_DIR)"

restore: config
	@test -n "$(BACKUP_DIR)" || (echo "BACKUP_DIR is required" >&2; exit 2)
	@test -n "$(RESTORE_DIR)" || (echo "RESTORE_DIR is required" >&2; exit 2)
	@scripts/restore.sh "$(BACKUP_DIR)" "$(RESTORE_DIR)" $(if $(RESTORE_DB),--database-name $(RESTORE_DB),)

feature: config
	@test -n "$(SECURITY_ID)" || (echo "SECURITY_ID is required" >&2; exit 2)
	@test -n "$(DECISION_AT)" || (echo "DECISION_AT is required" >&2; exit 2)
	@test -n "$(CALENDAR_PIN)" || (echo "CALENDAR_PIN is required" >&2; exit 2)
	@test -f "$(or $(TAXONOMY_REGISTRY),$(CURDIR)/schemas/feature-taxonomy-registry.json)" || (echo "TAXONOMY_REGISTRY is required" >&2; exit 2)
	@$(COMPOSE) run --rm --no-deps \
		-v "$(or $(TAXONOMY_REGISTRY),$(CURDIR)/schemas/feature-taxonomy-registry.json):/tmp/feature-taxonomy-registry.json:ro" \
		jupyter python -m research.feature_cli publish \
		--data-root /data \
		--features-root /data/features \
		--security-id "$(SECURITY_ID)" \
		--decision-at "$(DECISION_AT)" \
		--calendar-pin "$(CALENDAR_PIN)" \
		--feature-set "$(or $(FEATURE_SET),market-basic)" \
		--feature-set-version "$(or $(FEATURE_SET_VERSION),1.0.0)" \
		$(if $(ISSUER_ID),--issuer-id "$(ISSUER_ID)",) \
		--taxonomy-registry /tmp/feature-taxonomy-registry.json \
		$(if $(MACRO_SOURCE),--macro-source "$(MACRO_SOURCE)",) \
		$(if $(MACRO_SERIES_ID),--macro-series-id "$(MACRO_SERIES_ID)",) \
		$(if $(MACRO_GEOGRAPHY),--macro-geography "$(MACRO_GEOGRAPHY)",) \
		$(if $(MACRO_UNIT),--macro-unit "$(MACRO_UNIT)",) \
		$(if $(MACRO_FREQUENCY),--macro-frequency "$(MACRO_FREQUENCY)",) \
		--computation-delay-seconds "$(or $(FEATURE_DELAY),0)" \
		--git-commit "$(or $(INVS_GIT_COMMIT),unknown)"

feature-validate: config
	@test -n "$(FEATURE_MANIFEST)" || (echo "FEATURE_MANIFEST is required" >&2; exit 2)
	@$(COMPOSE) run --rm --no-deps jupyter python -m research.feature_cli validate \
		--manifest "$(FEATURE_MANIFEST)"

feature-batch: config
	@test -f "$(or $(FEATURE_REGISTRY),$(CURDIR)/schemas/feature-set-registry.json)" || (echo "FEATURE_REGISTRY is required" >&2; exit 2)
	@test -f "$(BATCH_UNIVERSE)" || (echo "BATCH_UNIVERSE is required" >&2; exit 2)
	@test -f "$(BATCH_SCHEDULE)" || (echo "BATCH_SCHEDULE is required" >&2; exit 2)
	@test -f "$(CALENDAR_PIN)" || (echo "CALENDAR_PIN is required" >&2; exit 2)
	@$(COMPOSE) run --rm --no-deps \
		-v "$(or $(FEATURE_REGISTRY),$(CURDIR)/schemas/feature-set-registry.json):/tmp/feature-set-registry.json:ro" \
		-v "$(BATCH_UNIVERSE):/tmp/feature-universe.json:ro" \
		-v "$(BATCH_SCHEDULE):/tmp/feature-schedule.json:ro" \
		-v "$(CALENDAR_PIN):/tmp/calendar-pin.json:ro" \
		-v "$(or $(TAXONOMY_REGISTRY),$(CURDIR)/schemas/feature-taxonomy-registry.json):/tmp/feature-taxonomy-registry.json:ro" \
		$(if $(SECURITY_MAPPINGS),-v "$(SECURITY_MAPPINGS):/tmp/feature-security-mappings.json:ro",) \
		jupyter python -m research.feature_cli batch-publish \
			--data-root /data \
			--features-root /data/features \
			--registry /tmp/feature-set-registry.json \
			--universe /tmp/feature-universe.json \
			--schedule /tmp/feature-schedule.json \
			--calendar-pin /tmp/calendar-pin.json \
			--taxonomy-registry /tmp/feature-taxonomy-registry.json \
			$(if $(SECURITY_MAPPINGS),--security-mappings /tmp/feature-security-mappings.json,) \
			--feature-set "$(or $(FEATURE_SET),market-basic)" \
			--feature-set-version "$(or $(FEATURE_SET_VERSION),1.0.0)" \
			$(if $(MACRO_SOURCE),--macro-source "$(MACRO_SOURCE)",) \
			$(if $(MACRO_SERIES_ID),--macro-series-id "$(MACRO_SERIES_ID)",) \
			$(if $(MACRO_GEOGRAPHY),--macro-geography "$(MACRO_GEOGRAPHY)",) \
			$(if $(MACRO_UNIT),--macro-unit "$(MACRO_UNIT)",) \
			$(if $(MACRO_FREQUENCY),--macro-frequency "$(MACRO_FREQUENCY)",) \
			--git-commit "$(or $(INVS_GIT_COMMIT),unknown)"

feature-batch-validate: config
	@test -n "$(BATCH_MANIFEST)" || (echo "BATCH_MANIFEST is required" >&2; exit 2)
	@test -f "$(or $(FEATURE_REGISTRY),$(CURDIR)/schemas/feature-set-registry.json)" || (echo "FEATURE_REGISTRY is required" >&2; exit 2)
	@$(COMPOSE) run --rm --no-deps \
		-v "$(or $(FEATURE_REGISTRY),$(CURDIR)/schemas/feature-set-registry.json):/tmp/feature-set-registry.json:ro" \
		jupyter python -m research.feature_cli batch-validate \
			--manifest "$(BATCH_MANIFEST)" \
				--features-root /data/features \
				--registry /tmp/feature-set-registry.json

feature-catalog: migrate
	@test -n "$(BATCH_MANIFEST)" || (echo "BATCH_MANIFEST is required" >&2; exit 2)
	@test -f "$(or $(FEATURE_REGISTRY),$(CURDIR)/schemas/feature-set-registry.json)" || (echo "FEATURE_REGISTRY is required" >&2; exit 2)
	@$(COMPOSE) run --rm --no-deps \
		-v "$(or $(FEATURE_REGISTRY),$(CURDIR)/schemas/feature-set-registry.json):/tmp/feature-set-registry.json:ro" \
		jupyter python -m research.feature_cli batch-validate \
			--manifest "$(BATCH_MANIFEST)" \
			--features-root /data/features \
			--registry /tmp/feature-set-registry.json
	@$(COMPOSE) --profile collect run --rm --build --entrypoint invs-feature-catalog collector \
		--manifest "$(BATCH_MANIFEST)" \
		--features-root /data/features

feature-report: migrate
	@$(COMPOSE) --profile collect run --rm --build --entrypoint invs-feature-report collector \
		$(if $(FEATURE_ARTIFACT_ID),--artifact-id "$(FEATURE_ARTIFACT_ID)",) \
		$(if $(FEATURE_SET),--feature-set "$(FEATURE_SET)",) \
		$(if $(FEATURE_SET_VERSION),--feature-set-version "$(FEATURE_SET_VERSION)",) \
		$(if $(FEATURE_REPORT_JSON),--json,) \
		$(if $(FEATURE_REPORT_FAIL_ON_ISSUES),--fail-on-issues,)

action-snapshot: config
	@test -n "$(DATA_SOURCE_ID)" || (echo "DATA_SOURCE_ID is required" >&2; exit 2)
	@test -n "$(SECURITY_ID)" || (echo "SECURITY_ID is required" >&2; exit 2)
	@test -n "$(DECISION_AT)" || (echo "DECISION_AT is required" >&2; exit 2)
	@test -n "$(ACTIONS_FILE)" || (echo "ACTIONS_FILE is required" >&2; exit 2)
	@test ! -e "$(ACTIONS_FILE)" || (echo "refusing to overwrite ACTIONS_FILE" >&2; exit 2)
	@umask 077; temporary="$(ACTIONS_FILE).tmp"; \
		$(COMPOSE) run --rm --no-deps --entrypoint invs-action-snapshot collector \
		$(if $(ACTION_DATABASE_URL),--database-url "$(ACTION_DATABASE_URL)",) \
		--data-source-id "$(DATA_SOURCE_ID)" \
		--security-id "$(SECURITY_ID)" \
		--decision-at "$(DECISION_AT)" > "$$temporary" && \
		mv "$$temporary" "$(ACTIONS_FILE)" || { status=$$?; rm -f "$$temporary"; exit $$status; }

adjust: config
	@test -n "$(RAW_PRICE_MANIFEST)" || (echo "RAW_PRICE_MANIFEST is required" >&2; exit 2)
	@test -n "$(ACTIONS_FILE)" || (echo "ACTIONS_FILE is required" >&2; exit 2)
	@test -n "$(DECISION_AT)" || (echo "DECISION_AT is required" >&2; exit 2)
	@$(COMPOSE) run --rm --no-deps jupyter python -m research.adjustment_cli publish \
		--raw-manifest "$(RAW_PRICE_MANIFEST)" \
		--actions "$(ACTIONS_FILE)" \
		--decision-at "$(DECISION_AT)" \
		--adjustments-root /data/adjusted/prices

adjust-validate: config
	@test -n "$(ADJUSTMENT_MANIFEST)" || (echo "ADJUSTMENT_MANIFEST is required" >&2; exit 2)
	@$(COMPOSE) run --rm --no-deps jupyter python -m research.adjustment_cli validate \
		--manifest "$(ADJUSTMENT_MANIFEST)"

bias-audit:
	@test -n "$(AUDIT_SPEC)" || (echo "AUDIT_SPEC is required" >&2; exit 2)
	@PYTHONPATH=python/research python3 python/research/bias_audit_cli.py publish \
		--spec "$(AUDIT_SPEC)" \
		--audits-root "$(or $(AUDITS_ROOT),data/audits/point-in-time)"

bias-audit-validate:
	@test -n "$(AUDIT_MANIFEST)" || (echo "AUDIT_MANIFEST is required" >&2; exit 2)
	@PYTHONPATH=python/research python3 python/research/bias_audit_cli.py validate \
		--manifest "$(AUDIT_MANIFEST)"

test: config
	@go test ./...
	@go vet ./...
	@python3 schemas/validate_schemas.py
	@$(COMPOSE) run --rm --no-deps \
		-v "$(CURDIR)/docker:/repo/docker:ro" \
		-v "$(CURDIR)/schemas:/repo/schemas:ro" \
		-e INVS_DASHBOARD_PATH=/repo/docker/grafana/dashboards/market-overview.json \
		-e INVS_SCHEMA_ROOT=/repo/schemas \
		jupyter sh -c "pip install -q -e '.[dev]' && python -m pytest && python -m ruff check research tests"

notebook: config
	@$(COMPOSE) run --rm --no-deps jupyter python -m jupyter nbconvert \
		--to notebook --execute --ExecutePreprocessor.timeout=120 \
		--output /tmp/vertical-slice.executed.ipynb notebooks/vertical_slice.ipynb

dashboard-smoke: migrate
	@python3 python/research/dashboard_smoke.py $(DASHBOARDS) >/dev/null
	@python3 python/research/dashboard_smoke.py $(DASHBOARDS) | \
		$(COMPOSE) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -v ON_ERROR_STOP=1'

validate: test notebook dashboard-smoke
	@$(COMPOSE) build postgres collector jupyter

down:
	@$(COMPOSE) down --remove-orphans

clean:
	@$(COMPOSE) down --volumes --remove-orphans
