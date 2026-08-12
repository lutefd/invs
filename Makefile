SHELL := /bin/sh

LOCAL_UID ?= $(shell id -u)
LOCAL_GID ?= $(shell id -g)
INVS_GIT_COMMIT ?= $(shell git rev-parse --verify HEAD 2>/dev/null | tr '[:upper:]' '[:lower:]')
COMPOSE = LOCAL_UID=$(LOCAL_UID) LOCAL_GID=$(LOCAL_GID) INVS_GIT_COMMIT=$(INVS_GIT_COMMIT) docker compose
SOURCE ?= all
RUN_KEY ?=
RUN_KEY_ARG = $(if $(RUN_KEY),--run-key $(RUN_KEY),)
DASHBOARDS := $(wildcard docker/grafana/dashboards/*.json)

.PHONY: setup config up migrate health urls ingest rerun test notebook dashboard-smoke validate down clean

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

health:
	@$(COMPOSE) ps
	@$(COMPOSE) exec -T postgres sh -c 'pg_isready -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"'
	@$(COMPOSE) exec -T jupyter python -c "import socket; socket.create_connection(('127.0.0.1', 8888), 2).close()"
	@$(COMPOSE) exec -T grafana wget -q -O /dev/null http://127.0.0.1:3000/api/health

urls:
	@$(COMPOSE) exec -T jupyter jupyter server list
	@echo "Grafana: http://127.0.0.1:$${GRAFANA_PORT:-3000}"

ingest: setup config
	@$(COMPOSE) --profile collect run --rm collector --source $(SOURCE) $(RUN_KEY_ARG)

rerun:
	@run_key="operator-retry-$(SOURCE)-$$(date +%s)"; \
		$(MAKE) ingest SOURCE=$(SOURCE) RUN_KEY="$$run_key" && \
		$(MAKE) ingest SOURCE=$(SOURCE) RUN_KEY="$$run_key"

test: config
	@go test ./...
	@go vet ./...
	@python3 schemas/validate_schemas.py
	@$(COMPOSE) run --rm --no-deps \
		-v "$(CURDIR)/docker:/repo/docker:ro" \
		-e INVS_DASHBOARD_PATH=/repo/docker/grafana/dashboards/market-overview.json \
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
