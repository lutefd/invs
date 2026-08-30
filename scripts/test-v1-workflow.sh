#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

make research-acceptance
make backtest-reproduction
make paper-reproduction

temporary_root=$(mktemp -d)
acceptance_root="$repo_root/data/research/acceptance/v1"
mkdir -p "$acceptance_root"
trap 'rm -rf "$temporary_root"' EXIT

python3 - "$repo_root" "$temporary_root" <<'PY'
import hashlib
import json
import sys
from pathlib import Path

repo_root = Path(sys.argv[1])
temporary_root = Path(sys.argv[2])


def ref(relative: str) -> dict[str, str]:
    path = repo_root / relative
    return {"path": relative, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}


backtest_report_path = repo_root / "data/research/acceptance/v0.5/reproduction/backtest-reproduction.json"
backtest_report = json.loads(backtest_report_path.read_text(encoding="utf-8"))
experiment_ids = {
    item["name"]: item["experiment_id"]
    for item in backtest_report["experiments"]
}


common = {
    "$schema": "../schemas/research-workflow.schema.json",
    "schema_version": "1.0.0",
    "decision_at": "2026-08-29T12:00:00Z",
    "research_report": ref("data/research/acceptance/v0.4/acceptance-artifacts.json"),
    "backtest_report": ref("data/research/acceptance/v0.5/reproduction/backtest-reproduction.json"),
    "paper_report": ref("data/research/acceptance/v0.6/reproduction/paper-reproduction.json"),
    "forward_record": {"status": "recorded_replay", "evidence": None},
    "limitations": [],
}

thematic = {
    **common,
    "workflow_id": "80000000-0000-4000-8000-000000000001",
    "scenario": "thematic",
    "backtest_experiment_ids": [
        experiment_ids["us-equal-weight-zero"],
        experiment_ids["us-momentum-zero"],
    ],
    "paper_account_ids": ["40000000-0000-4000-8000-000000000101"],
    "commodity_evidence": None,
}
cross_market = {
    **common,
    "workflow_id": "80000000-0000-4000-8000-000000000002",
    "scenario": "cross_market",
    "backtest_experiment_ids": sorted(experiment_ids.values()),
    "paper_account_ids": [
        "40000000-0000-4000-8000-000000000101",
        "40000000-0000-4000-8000-000000000102",
    ],
    "commodity_evidence": ref("fixtures/research/cross-market-commodity.json"),
}
for name, document in (("thematic", thematic), ("cross-market", cross_market)):
    (temporary_root / f"{name}.json").write_text(
        json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
PY

compose=(docker compose --progress quiet)
"${compose[@]}" run --rm --no-deps \
	-v "$repo_root:/repo:ro" \
	-v "$temporary_root:/tmp/workflow-specs:ro" \
	-v "$acceptance_root:/tmp/workflow-output" \
	jupyter sh -c "pip install -q -e '.[dev]' && \
	python -m research.workflow_cli build \
		--spec /tmp/workflow-specs/thematic.json --repo-root /repo \
		--output /tmp/workflow-output/thematic-workflow.json && \
	python -m research.workflow_cli build \
		--spec /tmp/workflow-specs/cross-market.json --repo-root /repo \
		--output /tmp/workflow-output/cross-market-workflow.json && \
	python -m research.workflow_cli validate \
		--report /tmp/workflow-output/thematic-workflow.json --repo-root /repo \
		--spec /tmp/workflow-specs/thematic.json && \
	python -m research.workflow_cli validate \
		--report /tmp/workflow-output/cross-market-workflow.json --repo-root /repo \
		--spec /tmp/workflow-specs/cross-market.json"

jq -e '
  .status == "attention" and
  .forward_record_status == "recorded_replay" and
  ([.checks[] | select(.check_id == "forward-record" and .status == "attention")] | length == 1)
' "$acceptance_root/thematic-workflow.json" >/dev/null
jq -e '
  .status == "attention" and
  ([.links.regions[]] | index("US")) != null and
  ([.links.regions[]] | index("BR")) != null and
  ([.checks[] | select(.check_id == "commodity-evidence" and .status == "passed")] | length == 1) and
  ([.checks[] | select(.check_id == "commodity-fitness" and .status == "attention")] | length == 1)
' "$acceptance_root/cross-market-workflow.json" >/dev/null

printf '%s\n' "v1 integrated workflow acceptance passed with explicit attention status"
printf '%s\n' "thematic report: $acceptance_root/thematic-workflow.json"
printf '%s\n' "cross-market report: $acceptance_root/cross-market-workflow.json"
